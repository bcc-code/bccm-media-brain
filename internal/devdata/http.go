package devdata

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/bcc-media/mediabrain/internal/domain"
	"github.com/bcc-media/mediabrain/internal/vidispineimport"
)

//go:embed ui/index.html
var uiFS embed.FS

// Backend is the subset of Store used by the HTTP handler.
type Backend interface {
	Apply(ctx context.Context, name string, req ApplyRequest) (Result, error)
	Reset(ctx context.Context) error
	Stats(ctx context.Context) ([]TableStat, error)
	Users(ctx context.Context) ([]User, error)
	CreateUser(ctx context.Context, email string, admin bool) (User, error)
	VidispineConfigured() bool
	VidispineImport(ctx context.Context, in vidispineimport.ImportInput) (VidispineAsset, error)
	VidispineRefresh(ctx context.Context, assetID string) (VidispineAsset, error)
}

// Handler serves the debug UI and its JSON API. Mount it with
// http.StripPrefix("/debug", ...). Routes (relative):
//
//	GET  /                       debug UI
//	GET  /api/scenarios          scenario specs
//	POST /api/scenarios/{name}   run a scenario (body: ApplyRequest)
//	GET  /api/stats              row count per table
//	POST /api/reset              truncate all tables
//	GET  /api/users              users to act as
//	POST /api/users              create a user (body: {"email": ..., "admin": ...})
//	GET  /api/vidispine            whether Vidispine is configured
//	POST /api/vidispine/import     import one item (body: {"itemId": ..., "typeId": ..., "projectId": ...})
//	POST /api/vidispine/refresh    refresh an imported asset (body: {"assetId": ...})
func Handler(b Backend) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		page, _ := uiFS.ReadFile("ui/index.html") // embedded; cannot fail
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(page)
	})

	mux.HandleFunc("GET /api/scenarios", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, Scenarios())
	})

	mux.HandleFunc("POST /api/scenarios/{name}", func(w http.ResponseWriter, r *http.Request) {
		var req ApplyRequest
		if r.ContentLength != 0 {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeErr(w, http.StatusBadRequest, err)
				return
			}
		}
		res, err := b.Apply(r.Context(), r.PathValue("name"), req)
		switch {
		case errors.Is(err, ErrUnknownScenario):
			writeErr(w, http.StatusNotFound, err)
		case errors.Is(err, ErrInvalidParams):
			writeErr(w, http.StatusBadRequest, err)
		case err != nil:
			writeErr(w, http.StatusInternalServerError, err)
		default:
			writeJSON(w, http.StatusOK, res)
		}
	})

	mux.HandleFunc("GET /api/stats", func(w http.ResponseWriter, r *http.Request) {
		stats, err := b.Stats(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, stats)
	})

	mux.HandleFunc("POST /api/reset", func(w http.ResponseWriter, r *http.Request) {
		if err := b.Reset(r.Context()); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	mux.HandleFunc("GET /api/users", func(w http.ResponseWriter, r *http.Request) {
		users, err := b.Users(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, users)
	})

	mux.HandleFunc("POST /api/users", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Email string `json:"email"`
			Admin bool   `json:"admin"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		u, err := b.CreateUser(r.Context(), req.Email, req.Admin)
		switch {
		case errors.Is(err, domain.ErrInvalidInput), errors.Is(err, domain.ErrConflict):
			writeErr(w, http.StatusBadRequest, err)
		case err != nil:
			writeErr(w, http.StatusInternalServerError, err)
		default:
			writeJSON(w, http.StatusOK, u)
		}
	})

	mux.HandleFunc("GET /api/vidispine", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"configured": b.VidispineConfigured()})
	})

	mux.HandleFunc("POST /api/vidispine/import", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ItemID    string `json:"itemId"`
			TypeID    int16  `json:"typeId"`
			ProjectID string `json:"projectId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		in := vidispineimport.ImportInput{ItemID: req.ItemID, TypeID: req.TypeID}
		if req.ProjectID != "" {
			in.ProjectID = &req.ProjectID
		}
		a, err := b.VidispineImport(r.Context(), in)
		writeResult(w, a, err)
	})

	mux.HandleFunc("POST /api/vidispine/refresh", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			AssetID string `json:"assetId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		a, err := b.VidispineRefresh(r.Context(), req.AssetID)
		writeResult(w, a, err)
	})

	return mux
}

// writeResult answers with v, or with the status that fits a domain error.
func writeResult(w http.ResponseWriter, v any, err error) {
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, v)
	case errors.Is(err, domain.ErrInvalidInput):
		writeErr(w, http.StatusBadRequest, err)
	case errors.Is(err, domain.ErrNotFound):
		writeErr(w, http.StatusNotFound, err)
	case errors.Is(err, domain.ErrConflict):
		writeErr(w, http.StatusConflict, err)
	case errors.Is(err, domain.ErrUnavailable):
		writeErr(w, http.StatusServiceUnavailable, err)
	default:
		writeErr(w, http.StatusInternalServerError, err)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
