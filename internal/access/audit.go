package access

import (
	"context"
	"log/slog"
	"sync"
)

// AuditChange is the log message of every audit record of a change that was made.
const AuditChange = "audit: change"

type auditKey struct{}

type heldAudit struct {
	mu       sync.Mutex
	released bool
	records  [][]any
}

// Audited writes the audit record of a change when err is nil, and returns err. Services call
// it on the result of a write, so the record says what happened, not what was asked for.
// attrs are slog key/value pairs; never pass tokens or free-form metadata.
func Audited(ctx context.Context, err error, action, target string, attrs ...any) error {
	if err != nil {
		return err
	}
	p, _ := From(ctx)
	record := []any{"action", action, "user", "system"}
	if p.UserID != "" {
		record[3] = p.UserID
	}
	if p.TokenID != "" {
		record = append(record, "token", p.TokenID)
	}
	record = append(append(record, "target", target), attrs...)
	record = append(record, OriginAttrs(ctx)...)
	if h, ok := ctx.Value(auditKey{}).(*heldAudit); ok {
		h.mu.Lock()
		defer h.mu.Unlock()
		if !h.released {
			h.records = append(h.records, record)
			return nil
		}
	}
	slog.InfoContext(ctx, AuditChange, record...)
	return nil
}

// HoldAudit keeps the records made with the returned context until release is called, which
// writes them (commit) or drops them. Code that runs several services in one transaction uses
// it, because their writes have only happened once that transaction commits. Inside another
// hold it does nothing: the outer one decides.
func HoldAudit(ctx context.Context) (context.Context, func(commit bool)) {
	if _, ok := ctx.Value(auditKey{}).(*heldAudit); ok {
		return ctx, func(bool) {}
	}
	h := &heldAudit{}
	held := context.WithValue(ctx, auditKey{}, h)
	return held, func(commit bool) {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.released {
			return
		}
		h.released = true
		if commit {
			for _, r := range h.records {
				slog.InfoContext(ctx, AuditChange, r...)
			}
		}
		h.records = nil
	}
}

// WithoutAudit drops every record made with the returned context. For generated dev data.
func WithoutAudit(ctx context.Context) context.Context {
	held, _ := HoldAudit(ctx)
	return held
}
