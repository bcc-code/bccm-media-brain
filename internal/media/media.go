// Package media serves the media of an asset to the browser. So far that is the preview of a
// rendition imported from Vidispine, streamed from Vidispine: MediaBrain stores no media and
// reads no storage itself.
package media

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/bcc-media/mediabrain/internal/asset"
	"github.com/bcc-media/mediabrain/internal/db"
	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/domain"
	"github.com/bcc-media/mediabrain/internal/vidispineimport"
	"github.com/bcc-media/mediabrain/pkg/vidispine"
)

// Opener reads a file's content from Vidispine. *vidispine.Client implements it.
type Opener interface {
	OpenFile(ctx context.Context, fileID, rangeHeader string) (*vidispine.FileStream, error)
}

type Service struct {
	q      sqlc.Querier
	assets *asset.Service
	src    Opener
}

// NewService takes a nil src when Vidispine is not configured; previews are then unavailable.
func NewService(q sqlc.Querier, tx db.TxRunner, src Opener) *Service {
	return &Service{q: q, assets: asset.NewService(q, tx), src: src}
}

// Preview opens the preview of a rendition, or the part of it rangeHeader asks for. Anyone who
// may view the asset may watch it. A rendition that is not a preview, or one that did not come
// from Vidispine, has none: NOT_FOUND, as for a rendition the caller may not see.
func (s *Service) Preview(ctx context.Context, renditionID, rangeHeader string) (*vidispine.FileStream, error) {
	r, err := s.assets.Rendition(ctx, renditionID)
	if err != nil {
		return nil, err
	}
	fileID := vidispineFileID(r.Metadata)
	if r.Kind != sqlc.RenditionKindEncode || r.Purpose == nil || *r.Purpose != vidispineimport.PreviewPurpose || fileID == "" {
		return nil, domain.ErrNotFound
	}
	if s.src == nil {
		return nil, domain.Unavailable("Vidispine is not configured")
	}
	// Only a byte range is passed on: anything else in the header is not ours to forward.
	if !strings.HasPrefix(rangeHeader, "bytes=") {
		rangeHeader = ""
	}
	st, err := s.src.OpenFile(ctx, fileID, rangeHeader)
	if errors.Is(err, vidispine.ErrNotFound) {
		slog.WarnContext(ctx, "media: preview missing in Vidispine", "rendition", renditionID, "file", fileID, "error", err)
		return nil, domain.ErrNotFound
	}
	if err != nil {
		slog.ErrorContext(ctx, "media: reading the preview from Vidispine", "rendition", renditionID, "file", fileID, "error", err)
		return nil, domain.Unavailable("Vidispine did not answer")
	}
	// Vidispine often calls a video application/octet-stream, which a browser may refuse to
	// play; the file's registered type says what it is.
	if t := st.Header.Get("Content-Type"); t == "" || t == "application/octet-stream" {
		if mime := s.mimeType(ctx, renditionID); mime != "" {
			st.Header.Set("Content-Type", mime)
		}
	}
	return st, nil
}

// mimeType is the registered type of the rendition's video file, or "".
func (s *Service) mimeType(ctx context.Context, renditionID string) string {
	files, err := s.q.ListRenditionFiles(ctx, []string{renditionID})
	if err != nil {
		slog.WarnContext(ctx, "media: reading the preview's file", "rendition", renditionID, "error", err)
		return ""
	}
	for _, f := range files {
		if f.Role != sqlc.FileRoleVideo {
			continue
		}
		file, err := s.q.GetFile(ctx, f.FileID)
		if err == nil && file.MimeType != nil {
			return *file.MimeType
		}
	}
	return ""
}

func vidispineFileID(metadata json.RawMessage) string {
	var m map[string]struct {
		FileID string `json:"fileId"`
	}
	_ = json.Unmarshal(metadata, &m)
	return m[vidispineimport.MetadataKey].FileID
}

// Previewer is what Handler needs of the Service.
type Previewer interface {
	Preview(ctx context.Context, renditionID, rangeHeader string) (*vidispine.FileStream, error)
}

// Handler serves GET /media/renditions/{id}: the rendition's preview, with byte ranges so a
// video element can seek. It must run behind the sign-in middleware.
func Handler(p Previewer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st, err := p.Preview(r.Context(), r.PathValue("id"), r.Header.Get("Range"))
		if err != nil {
			status, msg := statusOf(err)
			if status == http.StatusInternalServerError {
				slog.ErrorContext(r.Context(), "media: preview failed", "error", err)
			}
			http.Error(w, msg, status)
			return
		}
		defer st.Body.Close()
		for name, values := range st.Header {
			w.Header()[name] = values
		}
		// The content is the caller's to see, so no shared cache may keep it.
		w.Header().Set("Cache-Control", "private, max-age=300")
		w.WriteHeader(st.Status)
		if r.Method == http.MethodHead {
			return
		}
		if _, err := io.Copy(w, st.Body); err != nil && r.Context().Err() == nil {
			slog.WarnContext(r.Context(), "media: preview stream ended early", "error", err)
		}
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
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return http.StatusServiceUnavailable, "preview unavailable"
	}
	return http.StatusInternalServerError, "internal error"
}
