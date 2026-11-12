package graph

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/bcc-media/mediabrain/internal/access"
	"github.com/bcc-media/mediabrain/internal/domain"
	"github.com/bcc-media/mediabrain/internal/graph/generated"
)

var errorCodes = []struct {
	err  error
	code string
}{
	{domain.ErrNotFound, "NOT_FOUND"},
	{domain.ErrInvalidInput, "INVALID_INPUT"},
	{domain.ErrConflict, "CONFLICT"},
	{domain.ErrForbidden, "FORBIDDEN"},
	{domain.ErrUnauthenticated, "UNAUTHENTICATED"},
	{domain.ErrUnavailable, "UNAVAILABLE"},
	{domain.ErrTimeout, "TIMEOUT"},
}

// NewServer builds the GraphQL handler. Introspection is for development only.
func NewServer(r *Resolver, introspection bool) *handler.Server {
	srv := handler.New(generated.NewExecutableSchema(generated.Config{Resolvers: r, Complexity: complexity()}))
	srv.AddTransport(transport.POST{})
	srv.Use(extension.FixedComplexityLimit(ComplexityLimit))
	if introspection {
		srv.Use(extension.Introspection{})
	}
	srv.AroundOperations(func(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
		return next(withLoaders(ctx, r))
	})
	srv.SetErrorPresenter(presentError)
	return srv
}

// caller is who a refusal is logged about, named the way access.Audited names who made a change.
func caller(ctx context.Context) []any {
	p, _ := access.From(ctx)
	if p.TokenID != "" {
		return []any{"user", p.UserID, "token", p.TokenID}
	}
	return []any{"user", p.UserID}
}

// presentError gives every error a code. Domain errors have their own, with the detail a client
// can act on; what gqlgen refused before a resolver ran keeps gqlgen's code; anything else is
// unexpected, and only its cause's log line says what it was.
func presentError(ctx context.Context, err error) *gqlerror.Error {
	out := graphql.DefaultErrorPresenter(ctx, err)
	for _, c := range errorCodes {
		if errors.Is(err, c.err) {
			if c.code == "FORBIDDEN" {
				slog.WarnContext(ctx, "audit: denied", append(caller(ctx), "path", out.Path.String())...)
			}
			out.Extensions = map[string]any{"code": c.code}
			d := domain.DetailOf(err)
			if d.Reason != "" {
				out.Extensions["reason"] = d.Reason
			}
			if d.Reason == domain.ReasonStaleRevision {
				out.Extensions["currentRevision"] = d.CurrentRevision
			}
			if d.Field != "" {
				out.Extensions["field"] = d.Field
			}
			return out
		}
	}
	if _, ok := out.Extensions["code"]; ok {
		return out
	}
	// The one thing gqlgen refuses during execution without a code of its own. __schema and
	// __type cannot fail in another way, so the path is enough to know it.
	if len(out.Path) > 0 && strings.HasPrefix(out.Path.String(), "__") {
		out.Extensions = map[string]any{"code": "INTROSPECTION_DISABLED"}
		return out
	}
	cause := err
	var internal internalError
	if errors.As(err, &internal) {
		cause = internal.cause
	}
	slog.ErrorContext(ctx, "graphql: internal error", "error", cause.Error(), "path", out.Path.String())
	out.Message = internalError{}.Error()
	out.Extensions = map[string]any{"code": "INTERNAL"}
	return out
}
