package graph

import (
	"context"
	"errors"

	"github.com/bcc-media/mediabrain/internal/domain"
	"github.com/bcc-media/mediabrain/internal/graph/model"
)

// internalError hides the cause of an unexpected failure from the client; the error presenter
// logs it. Every resolver returns its errors through one of the helpers below.
type internalError struct{ cause error }

func (internalError) Error() string { return "internal error" }

func mask(err error) error {
	if err == nil {
		return nil
	}
	// The request's deadline passed while a statement ran. The driver reports that as its own
	// error; the client should learn that the request was too slow, not that something broke.
	if errors.Is(err, context.DeadlineExceeded) {
		return domain.ErrTimeout
	}
	for _, c := range errorCodes {
		if errors.Is(err, c.err) {
			return err
		}
	}
	return internalError{cause: err}
}

func plain[T any](v T, err error) (T, error) { return v, mask(err) }

func one[T any](v T, err error) (*T, error) {
	if err != nil {
		return nil, mask(err)
	}
	return &v, nil
}

// found is one for lookups by id: a missing (or invisible) object is null, not an error.
func found[T any](v T, err error) (*T, error) {
	if errors.Is(err, domain.ErrNotFound) {
		return nil, nil
	}
	return one(v, err)
}

func many[T any](rows []T, err error) ([]*T, error) {
	if err != nil {
		return nil, mask(err)
	}
	out := make([]*T, len(rows))
	for i := range rows {
		out[i] = &rows[i]
	}
	return out, nil
}

func done(err error) (bool, error) { return err == nil, mask(err) }

// nodes is many for a page: the items as the generated connection types hold them, and where
// the page ends. A resolver puts the two into its connection type.
func nodes[T any](s domain.Slice[T], err error) ([]*T, *model.PageInfo, error) {
	if err != nil {
		return nil, nil, mask(err)
	}
	out := make([]*T, len(s.Nodes))
	for i := range s.Nodes {
		out[i] = &s.Nodes[i]
	}
	return out, &model.PageInfo{HasNextPage: s.HasNextPage, EndCursor: s.EndCursor}, nil
}

func deref(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
