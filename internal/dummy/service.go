// Package dummy is a PLACEHOLDER domain service demonstrating the layering
// (sqlc -> service -> GraphQL). Delete it when the real media model lands.
package dummy

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/bcc-media/mediabrain/internal/db/sqlc"
)

var (
	// ErrNotFound is returned when a dummy does not exist.
	ErrNotFound = errors.New("dummy: not found")
	// ErrInvalidInput is returned for invalid arguments.
	ErrInvalidInput = errors.New("dummy: invalid input")
)

const (
	DefaultLimit = 50
	MaxLimit     = 500
)

// Service implements dummy business logic.
type Service struct {
	q sqlc.Querier
}

// NewService returns a Service backed by q.
func NewService(q sqlc.Querier) *Service { return &Service{q: q} }

// Get returns a dummy by id.
func (s *Service) Get(ctx context.Context, id int64) (sqlc.Dummy, error) {
	d, err := s.q.GetDummy(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Dummy{}, ErrNotFound
	}
	if err != nil {
		return sqlc.Dummy{}, fmt.Errorf("dummy: get %d: %w", id, err)
	}
	return d, nil
}

// List returns dummies ordered by id. A limit of 0 means DefaultLimit.
func (s *Service) List(ctx context.Context, limit, offset int) ([]sqlc.Dummy, error) {
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 0 || limit > MaxLimit || offset < 0 {
		return nil, fmt.Errorf("%w: limit must be 1..%d and offset >= 0", ErrInvalidInput, MaxLimit)
	}
	ds, err := s.q.ListDummies(ctx, sqlc.ListDummiesParams{Limit: int32(limit), Offset: int32(offset)})
	if err != nil {
		return nil, fmt.Errorf("dummy: list: %w", err)
	}
	return ds, nil
}

// Create inserts a new dummy with the given (trimmed, non-empty) name.
func (s *Service) Create(ctx context.Context, name string) (sqlc.Dummy, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return sqlc.Dummy{}, fmt.Errorf("%w: name is required", ErrInvalidInput)
	}
	d, err := s.q.CreateDummy(ctx, name)
	if err != nil {
		return sqlc.Dummy{}, fmt.Errorf("dummy: create: %w", err)
	}
	return d, nil
}

// Delete removes a dummy by id.
func (s *Service) Delete(ctx context.Context, id int64) error {
	n, err := s.q.DeleteDummy(ctx, id)
	if err != nil {
		return fmt.Errorf("dummy: delete %d: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
