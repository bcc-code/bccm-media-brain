package access

import "context"

// Origin says which HTTP request a caller arrived with, so that an audit record can be matched
// to the request log and to where the request came from. The request log creates it; sign-in
// fills in the rest once it knows who is asking.
type Origin struct {
	// RequestID is unique per request and is also the X-Request-Id response header.
	RequestID string
	// Client is the address the request came from: what the proxy reports where one is
	// trusted to, otherwise the peer itself. It is recorded, never used to decide anything.
	Client string
	// User and Token are who the request was authenticated as; empty when it was refused.
	User, Token string
}

type originKey struct{}

func WithOrigin(ctx context.Context, o *Origin) context.Context {
	return context.WithValue(ctx, originKey{}, o)
}

// OriginOf returns the request's origin, or nil outside an HTTP request (command line, sweeper).
func OriginOf(ctx context.Context) *Origin {
	o, _ := ctx.Value(originKey{}).(*Origin)
	return o
}

// OriginAttrs are the log attributes that tie a record to its request.
func OriginAttrs(ctx context.Context) []any {
	o := OriginOf(ctx)
	if o == nil {
		return nil
	}
	var attrs []any
	if o.RequestID != "" {
		attrs = append(attrs, "request", o.RequestID)
	}
	if o.Client != "" {
		attrs = append(attrs, "client", o.Client)
	}
	return attrs
}
