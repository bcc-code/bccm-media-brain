// Package media serves the media of an asset to the browser. So far that is the preview of a
// rendition, read from a copy of its file on a storage location with a file:// address. That
// storage must be mounted on this server at the path the address names.
package media

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bcc-media/mediabrain/internal/asset"
	"github.com/bcc-media/mediabrain/internal/db"
	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/domain"
)

// PreviewPurpose is the purpose of a rendition that is a preview.
const PreviewPurpose = "preview"

type Service struct {
	q      sqlc.Querier
	assets *asset.Service
}

func NewService(q sqlc.Querier, tx db.TxRunner) *Service {
	return &Service{q: q, assets: asset.NewService(q, tx)}
}

// File is an open preview. The caller closes it.
type File struct {
	*os.File
	Name     string
	ModTime  time.Time
	MimeType string
}

// Preview opens the video file of a preview rendition (an encode rendition with purpose
// "preview"). Anyone who may view the asset may watch it. Any other rendition, or one the caller
// may not see, is NOT_FOUND. A preview none of whose copies can be read here is UNAVAILABLE.
func (s *Service) Preview(ctx context.Context, renditionID string) (*File, error) {
	r, err := s.assets.Rendition(ctx, renditionID)
	if err != nil {
		return nil, err
	}
	if r.Kind != sqlc.RenditionKindEncode || r.Purpose == nil || *r.Purpose != PreviewPurpose {
		return nil, domain.ErrNotFound
	}
	attached, err := s.q.ListRenditionFiles(ctx, []string{renditionID})
	if err != nil {
		return nil, err
	}
	var fileID string
	for _, a := range attached {
		if a.Role == sqlc.FileRoleVideo {
			fileID = a.FileID
			break
		}
	}
	if fileID == "" {
		return nil, domain.ErrNotFound
	}
	f, err := s.q.GetFile(ctx, fileID)
	if err != nil {
		return nil, domain.FromPG(err)
	}
	copies, err := s.q.ListFileCopies(ctx, []string{fileID})
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(copies))
	for i, c := range copies {
		ids[i] = c.LocationID
	}
	locations, err := s.q.GetStorageLocations(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]sqlc.StorageLocation, len(locations))
	for _, l := range locations {
		byID[l.ID] = l
	}
	for _, c := range copies {
		l, ok := byID[c.LocationID]
		if !ok || !l.Online || c.Status == sqlc.CopyStatusMissing {
			continue
		}
		path, ok := localPath(l.BaseUri, c.Path)
		if !ok {
			continue
		}
		opened, err := open(path)
		if err != nil {
			slog.WarnContext(ctx, "media: preview copy not readable", "rendition", renditionID, "copy", c.ID, "path", path, "error", err)
			continue
		}
		out := &File{File: opened.file, Name: filepath.Base(path), ModTime: opened.modTime}
		if f.MimeType != nil {
			out.MimeType = *f.MimeType
		}
		return out, nil
	}
	slog.WarnContext(ctx, "media: no readable copy of the preview", "rendition", renditionID, "file", fileID, "copies", len(copies))
	return nil, domain.Unavailable("the preview is on no storage this server can read")
}

type opened struct {
	file    *os.File
	modTime time.Time
}

func open(path string) (opened, error) {
	f, err := os.Open(path)
	if err != nil {
		return opened{}, err
	}
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = errors.New("not a regular file")
	}
	if err != nil {
		_ = f.Close()
		return opened{}, err
	}
	return opened{file: f, modTime: info.ModTime()}, nil
}

// localPath is where a copy lies on this server: the path of a file:// base address joined with
// the copy's relative path. Anything that would lead outside the base is refused, whatever the
// registered path says.
func localPath(baseURI, rel string) (string, bool) {
	u, err := url.Parse(baseURI)
	if err != nil || u.Scheme != "file" || (u.Host != "" && u.Host != "localhost") || u.Path == "" {
		return "", false
	}
	base := filepath.Clean(filepath.FromSlash(u.Path))
	path := filepath.Join(base, filepath.FromSlash(rel))
	if path == base || !strings.HasPrefix(path, base+string(filepath.Separator)) {
		return "", false
	}
	return path, true
}

// Previewer is what Handler needs of the Service.
type Previewer interface {
	Preview(ctx context.Context, renditionID string) (*File, error)
}

// Handler serves GET /media/renditions/{id}: the rendition's preview, with byte ranges so a
// video element can seek. It must run behind the sign-in middleware.
func Handler(p Previewer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, err := p.Preview(r.Context(), r.PathValue("id"))
		if err != nil {
			status, msg := statusOf(err)
			if status == http.StatusInternalServerError {
				slog.ErrorContext(r.Context(), "media: preview failed", "error", err)
			}
			http.Error(w, msg, status)
			return
		}
		defer f.Close()
		if f.MimeType != "" {
			w.Header().Set("Content-Type", f.MimeType)
		}
		// The content is the caller's to see, so no shared cache may keep it.
		w.Header().Set("Cache-Control", "private, max-age=300")
		http.ServeContent(w, r, f.Name, f.ModTime, f.File)
	})
}

// statusOf maps a domain error to an HTTP status. Forbidden is not-found here: the reader can
// view the asset or the rendition looks missing, as in the API.
func statusOf(err error) (int, string) {
	switch {
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrForbidden):
		return http.StatusNotFound, "not found"
	case errors.Is(err, domain.ErrUnauthenticated):
		return http.StatusUnauthorized, "unauthenticated"
	case errors.Is(err, domain.ErrUnavailable):
		return http.StatusServiceUnavailable, "preview unavailable"
	}
	return http.StatusInternalServerError, "internal error"
}
