package graph

import (
	"context"
	"sync"

	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/domain"
	"github.com/bcc-media/mediabrain/internal/graph/loader"
	"github.com/bcc-media/mediabrain/internal/subtitle"
)

// page is the first and after arguments of a nested list, as a map key: parents asked for the
// same page share one loader, and so one statement.
type page struct {
	first int
	after string
}

func window(first *int, after *string) page {
	p := page{first: deref(first)}
	if after != nil {
		p.after = *after
	}
	return p
}

// loaders batch the child lookups of one request. Every nested field goes through one of
// them, so resolving a list never runs a query per item.
type loaders struct {
	types          *loader.Loader[int16, sqlc.AssetType]
	levels         *loader.Loader[string, sqlc.PermissionLevel]
	links          *loader.Loader[string, []sqlc.AssetProject]
	renditions     *loader.Loader[string, []sqlc.Rendition]
	renditionFiles *loader.Loader[string, []sqlc.RenditionFile]
	files          *loader.Loader[string, sqlc.File]
	hashes         *loader.Loader[string, []sqlc.FileHash]
	copies         *loader.Loader[string, []sqlc.FileCopy]
	locations      *loader.Loader[string, sqlc.StorageLocation]
	programs       *loader.Loader[string, sqlc.Program]
	projects       *loader.Loader[string, sqlc.Project]

	// Loaders for fields with arguments, one per distinct argument value.
	mu              sync.Mutex
	r               *Resolver
	subs            *subtitle.Service
	versions        map[page]*loader.Loader[string, domain.Slice[sqlc.AssetVersion]]
	programProjects map[page]*loader.Loader[string, domain.Slice[sqlc.Project]]
	tracks          map[bool]*loader.Loader[string, []sqlc.SubtitleTrack]
	cues            map[page]*loader.Loader[string, domain.Slice[sqlc.ListSubtitleCuesRow]]
	exports         map[subtitle.Format]*loader.Loader[string, string]
}

func newLoaders(r *Resolver) *loaders {
	return &loaders{
		types: loader.New(func(ctx context.Context, _ []int16) (map[int16]sqlc.AssetType, error) {
			types, err := r.AssetsSvc.Types(ctx)
			out := make(map[int16]sqlc.AssetType, len(types))
			for _, t := range types {
				out[t.ID] = t
			}
			return out, err
		}),
		levels:         loader.New(r.AssetsSvc.LevelsFor),
		links:          loader.New(r.AssetsSvc.LinksFor),
		renditions:     loader.New(r.AssetsSvc.RenditionsFor),
		renditionFiles: loader.New(r.AssetsSvc.FilesFor),
		files:          loader.New(r.FilesSvc.ByID),
		hashes:         loader.New(r.FilesSvc.HashesFor),
		copies:         loader.New(r.FilesSvc.CopiesFor),
		locations:      loader.New(r.FilesSvc.LocationsByID),
		programs:       loader.New(r.ProjectsSvc.ProgramsByID),
		projects:       loader.New(r.ProjectsSvc.ProjectsByID),

		r:               r,
		subs:            r.SubtitlesSvc,
		versions:        map[page]*loader.Loader[string, domain.Slice[sqlc.AssetVersion]]{},
		programProjects: map[page]*loader.Loader[string, domain.Slice[sqlc.Project]]{},
		tracks:          map[bool]*loader.Loader[string, []sqlc.SubtitleTrack]{},
		cues:            map[page]*loader.Loader[string, domain.Slice[sqlc.ListSubtitleCuesRow]]{},
		exports:         map[subtitle.Format]*loader.Loader[string, string]{},
	}
}

func keyed[A comparable, V any](l *loaders, m map[A]*loader.Loader[string, V], arg A, fetch func(context.Context, []string) (map[string]V, error)) *loader.Loader[string, V] {
	l.mu.Lock()
	defer l.mu.Unlock()
	if m[arg] == nil {
		m[arg] = loader.New(fetch)
	}
	return m[arg]
}

func (l *loaders) versionsFor(p page) *loader.Loader[string, domain.Slice[sqlc.AssetVersion]] {
	return keyed(l, l.versions, p, func(ctx context.Context, ids []string) (map[string]domain.Slice[sqlc.AssetVersion], error) {
		return l.r.AssetsSvc.VersionsFor(ctx, ids, p.first, &p.after)
	})
}

func (l *loaders) programProjectsFor(p page) *loader.Loader[string, domain.Slice[sqlc.Project]] {
	return keyed(l, l.programProjects, p, func(ctx context.Context, ids []string) (map[string]domain.Slice[sqlc.Project], error) {
		return l.r.ProjectsSvc.ProjectsFor(ctx, ids, p.first, &p.after)
	})
}

func (l *loaders) tracksFor(history bool) *loader.Loader[string, []sqlc.SubtitleTrack] {
	return keyed(l, l.tracks, history, func(ctx context.Context, ids []string) (map[string][]sqlc.SubtitleTrack, error) {
		return l.subs.TracksFor(ctx, ids, history)
	})
}

func (l *loaders) cuesFor(p page) *loader.Loader[string, domain.Slice[sqlc.ListSubtitleCuesRow]] {
	return keyed(l, l.cues, p, func(ctx context.Context, ids []string) (map[string]domain.Slice[sqlc.ListSubtitleCuesRow], error) {
		return l.subs.CuesFor(ctx, ids, p.first, &p.after)
	})
}

func (l *loaders) exportFor(f subtitle.Format) *loader.Loader[string, string] {
	return keyed(l, l.exports, f, func(ctx context.Context, ids []string) (map[string]string, error) {
		return l.subs.ExportFor(ctx, ids, f)
	})
}

type loadersKey struct{}

func withLoaders(ctx context.Context, r *Resolver) context.Context {
	return context.WithValue(ctx, loadersKey{}, newLoaders(r))
}

func (r *Resolver) load(ctx context.Context) *loaders {
	if l, ok := ctx.Value(loadersKey{}).(*loaders); ok {
		return l
	}
	return newLoaders(r)
}
