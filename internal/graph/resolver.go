// Package graph contains the GraphQL resolvers.
package graph

//go:generate go tool gqlgen generate

import (
	"context"

	"github.com/bcc-media/mediabrain/internal/db/sqlc"
)

// DummyService is the subset of dummy.Service used by resolvers (PLACEHOLDER).
type DummyService interface {
	Get(ctx context.Context, id int64) (sqlc.Dummy, error)
	List(ctx context.Context, limit, offset int) ([]sqlc.Dummy, error)
	Create(ctx context.Context, name string) (sqlc.Dummy, error)
	Delete(ctx context.Context, id int64) error
}

// Resolver is the root resolver; it holds injected dependencies.
type Resolver struct {
	DummySvc DummyService
}
