package app

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"

	"github.com/bcc-media/mediabrain/internal/access"
)

// requestID is 64 random bits: enough to find one request in the logs, and it says nothing.
func requestID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the connection, for the write deadline of /query.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// Flush keeps streaming responses (e.g. GraphQL subscriptions later) working.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// logRequests logs every request with slog. Health checks log at debug level
// to keep production logs quiet.
func logRequests(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		origin := &access.Origin{RequestID: requestID()}
		w.Header().Set("X-Request-Id", origin.RequestID)
		r = r.WithContext(access.WithOrigin(r.Context(), origin))
		next.ServeHTTP(rec, r)
		level := slog.LevelInfo
		if r.URL.Path == "/healthz" {
			level = slog.LevelDebug
		}
		if rec.status >= 500 {
			level = slog.LevelError
		}
		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote", r.RemoteAddr,
			"request", origin.RequestID,
		}
		// Sign-in filled these in; a request that was refused, or needs no sign-in, has none.
		if origin.Client != "" {
			attrs = append(attrs, "client", origin.Client)
		}
		if origin.User != "" {
			attrs = append(attrs, "user", origin.User)
		}
		if origin.Token != "" {
			attrs = append(attrs, "token", origin.Token)
		}
		log.Log(r.Context(), level, "http request", attrs...)
	})
}
