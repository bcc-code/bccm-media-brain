// Package subtitle imports, edits and exports subtitle tracks. A track is never changed:
// every import or edit writes a new revision that points at the one before it.
package subtitle

import (
	"context"
	"errors"

	"github.com/bcc-media/mediabrain/internal/access"
	"github.com/bcc-media/mediabrain/internal/db"
	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/domain"
)

// MaxCuePage is the largest page of cues one request can ask for.
const MaxCuePage = 5000

type Service struct {
	q  sqlc.Querier
	tx db.TxRunner
}

func NewService(q sqlc.Querier, tx db.TxRunner) *Service { return &Service{q: q, tx: tx} }

type ImportInput struct {
	VersionID    string
	Language     *string
	Kind         *sqlc.TrackKind
	Format       Format
	Content      string
	SourceFileID *string
}

// Import parses subtitle text and stores it as the newest revision for its language on the
// given version. Each language has its own subtitle rendition, created on first import.
func (s *Service) Import(ctx context.Context, in ImportInput) (sqlc.SubtitleTrack, error) {
	p, err := access.From(ctx)
	if err != nil {
		return sqlc.SubtitleTrack{}, err
	}
	kind := sqlc.TrackKindSubtitle
	if in.Kind != nil {
		kind = *in.Kind
	}
	if !kind.Valid() {
		return sqlc.SubtitleTrack{}, domain.Invalid("unknown kind")
	}
	language := domain.Blank(in.Language)

	var track sqlc.SubtitleTrack
	err = s.tx.InTx(ctx, func(q sqlc.Querier) error {
		v, err := q.GetAssetVersion(ctx, in.VersionID)
		if err != nil {
			return domain.FromPG(err)
		}
		if err := access.RequireAsset(ctx, q, v.AssetID, access.Edit); err != nil {
			return err
		}
		cues, err := Parse(in.Format, in.Content)
		if err != nil {
			return err
		}
		if in.SourceFileID != nil {
			// The source file gets attached to the subtitle rendition below.
			if err := access.RequireFileReuse(ctx, q, *in.SourceFileID); err != nil {
				return err
			}
		}
		r, err := q.FindSubtitleRendition(ctx, sqlc.FindSubtitleRenditionParams{VersionID: v.ID, Language: language})
		if errors.Is(domain.FromPG(err), domain.ErrNotFound) {
			r, err = q.CreateRendition(ctx, sqlc.CreateRenditionParams{
				VersionID: v.ID, Kind: sqlc.RenditionKindSubtitle, Language: language,
				Overrides: []byte(`{}`), Metadata: []byte(`{}`),
			})
		}
		if err != nil {
			return domain.FromPG(err)
		}
		track, err = addRevision(ctx, q, sqlc.CreateSubtitleTrackParams{
			RenditionID: r.ID, AssetID: v.AssetID, SourceFileID: in.SourceFileID, Language: language,
			Kind: kind, Origin: sqlc.TrackOriginImport, CreatedBy: p.UserRef(),
		}, cues)
		if err != nil || in.SourceFileID == nil {
			return err
		}
		attached, err := q.ListRenditionFiles(ctx, []string{r.ID})
		if err != nil {
			return err
		}
		for _, f := range attached {
			if f.FileID == *in.SourceFileID && f.Role == sqlc.FileRoleSubtitle {
				return nil
			}
		}
		_, err = q.AttachFile(ctx, sqlc.AttachFileParams{RenditionID: r.ID, FileID: *in.SourceFileID, Role: sqlc.FileRoleSubtitle})
		return domain.FromPG(err)
	})
	return track, err
}

// addRevision stores a track after the rendition's latest one. Two writers racing for the same
// revision number collide on the unique key and one of them gets a conflict.
func addRevision(ctx context.Context, q sqlc.Querier, t sqlc.CreateSubtitleTrackParams, cues []Cue) (sqlc.SubtitleTrack, error) {
	t.Revision = 1
	latest, err := q.LatestSubtitleTrack(ctx, t.RenditionID)
	switch {
	case err == nil:
		t.Revision, t.ParentID = latest.Revision+1, &latest.ID
	case !errors.Is(domain.FromPG(err), domain.ErrNotFound):
		return sqlc.SubtitleTrack{}, err
	}
	track, err := q.CreateSubtitleTrack(ctx, t)
	if err != nil {
		return sqlc.SubtitleTrack{}, domain.FromPG(err)
	}
	rows := make([]sqlc.InsertSubtitleCuesParams, len(cues))
	for i, c := range cues {
		rows[i] = sqlc.InsertSubtitleCuesParams{
			TrackID: track.ID, AssetID: track.AssetID, Seq: int32(i + 1),
			StartMs: c.StartMs, EndMs: c.EndMs, Text: c.Text, Speaker: c.Speaker,
		}
	}
	_, err = q.InsertSubtitleCues(ctx, rows)
	return track, domain.FromPG(err)
}

// Edit replaces the cues of the latest revision by writing a new one. Editing an older
// revision is a conflict, so two editors cannot silently overwrite each other.
func (s *Service) Edit(ctx context.Context, trackID string, cues []Cue) (sqlc.SubtitleTrack, error) {
	p, err := access.From(ctx)
	if err != nil {
		return sqlc.SubtitleTrack{}, err
	}
	var track sqlc.SubtitleTrack
	err = s.tx.InTx(ctx, func(q sqlc.Querier) error {
		base, err := s.track(ctx, q, trackID, access.Edit)
		if err != nil {
			return err
		}
		cues, err := Normalize(cues)
		if err != nil {
			return err
		}
		latest, err := q.LatestSubtitleTrack(ctx, base.RenditionID)
		if err != nil {
			return domain.FromPG(err)
		}
		if latest.ID != base.ID {
			return domain.Conflict("track has a newer revision (%d)", latest.Revision)
		}
		track, err = addRevision(ctx, q, sqlc.CreateSubtitleTrackParams{
			RenditionID: base.RenditionID, AssetID: base.AssetID, Language: base.Language,
			Kind: base.Kind, Origin: sqlc.TrackOriginManual, CreatedBy: p.UserRef(),
		}, cues)
		return err
	})
	return track, err
}

func (s *Service) track(ctx context.Context, q sqlc.Querier, id string, need sqlc.PermissionLevel) (sqlc.SubtitleTrack, error) {
	t, err := q.GetSubtitleTrack(ctx, id)
	if err != nil {
		return sqlc.SubtitleTrack{}, domain.FromPG(err)
	}
	return t, access.RequireAsset(ctx, q, t.AssetID, need)
}

func (s *Service) Track(ctx context.Context, id string) (sqlc.SubtitleTrack, error) {
	return s.track(ctx, s.q, id, access.View)
}

// AssetTracks lists an asset's tracks: the latest revision per rendition, or all with history.
func (s *Service) AssetTracks(ctx context.Context, assetID string, history bool) ([]sqlc.SubtitleTrack, error) {
	if _, err := s.q.GetAsset(ctx, assetID); err != nil {
		return nil, domain.FromPG(err)
	}
	if err := access.RequireAsset(ctx, s.q, assetID, access.View); err != nil {
		return nil, err
	}
	tracks, err := s.q.ListAssetTracks(ctx, assetID)
	if err != nil {
		return nil, err
	}
	return latestOnly(tracks, history), nil
}

// latestOnly expects tracks ordered by rendition, newest revision first.
func latestOnly(tracks []sqlc.SubtitleTrack, history bool) []sqlc.SubtitleTrack {
	if history {
		return tracks
	}
	out := []sqlc.SubtitleTrack{}
	for i, t := range tracks {
		if i == 0 || tracks[i-1].RenditionID != t.RenditionID {
			out = append(out, t)
		}
	}
	return out
}

func (s *Service) Delete(ctx context.Context, id string) error {
	if _, err := s.track(ctx, s.q, id, access.Delete); err != nil {
		return err
	}
	_, err := s.q.DeleteSubtitleTrack(ctx, id)
	return domain.FromPGDelete(err)
}

// TracksFor and CuesFor load children for renditions and tracks the caller was already
// allowed to see; they are the batch side of the GraphQL dataloaders.
func (s *Service) TracksFor(ctx context.Context, renditionIDs []string, history bool) (map[string][]sqlc.SubtitleTrack, error) {
	rows, err := s.q.ListRenditionTracks(ctx, renditionIDs)
	out := map[string][]sqlc.SubtitleTrack{}
	for _, t := range latestOnly(rows, history) {
		out[t.RenditionID] = append(out[t.RenditionID], t)
	}
	return out, err
}

func (s *Service) CuesFor(ctx context.Context, trackIDs []string, limit, offset int) (map[string][]sqlc.ListSubtitleCuesRow, error) {
	l, o, err := domain.PageMax(limit, offset, MaxCuePage)
	if err != nil {
		return nil, err
	}
	return s.cues(ctx, trackIDs, l, o)
}

func (s *Service) cues(ctx context.Context, trackIDs []string, limit, offset int32) (map[string][]sqlc.ListSubtitleCuesRow, error) {
	rows, err := s.q.ListSubtitleCues(ctx, sqlc.ListSubtitleCuesParams{TrackIds: trackIDs, Limit: limit, Offset: offset})
	out := map[string][]sqlc.ListSubtitleCuesRow{}
	for _, r := range rows {
		out[r.TrackID] = append(out[r.TrackID], r)
	}
	return out, err
}

// ExportFor renders whole tracks as SRT or WebVTT text.
func (s *Service) ExportFor(ctx context.Context, trackIDs []string, format Format) (map[string]string, error) {
	rows, err := s.cues(ctx, trackIDs, MaxCues, 0)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(trackIDs))
	for _, id := range trackIDs {
		cues := make([]Cue, len(rows[id]))
		for i, r := range rows[id] {
			cues[i] = Cue{StartMs: r.StartMs, EndMs: r.EndMs, Text: r.Text, Speaker: r.Speaker}
		}
		if out[id], err = Render(format, cues); err != nil {
			return nil, err
		}
	}
	return out, nil
}
