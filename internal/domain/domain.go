// Package domain holds the errors and input rules shared by all domain services.
package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrNotFound        = errors.New("not found")
	ErrInvalidInput    = errors.New("invalid input")
	ErrConflict        = errors.New("conflict")
	ErrForbidden       = errors.New("forbidden")
	ErrUnauthenticated = errors.New("unauthenticated")
	// ErrUnavailable means a system MediaBrain depends on is not configured or did not answer.
	ErrUnavailable = errors.New("unavailable")
)

const (
	DefaultLimit = 50
	MaxLimit     = 500
)

func Invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidInput, fmt.Sprintf(format, a...))
}

func Conflict(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrConflict, fmt.Sprintf(format, a...))
}

func Unavailable(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrUnavailable, fmt.Sprintf(format, a...))
}

// FromPG maps database errors of reads, inserts and updates to domain errors.
func FromPG(err error) error { return fromPG(err, false) }

// FromPGDelete is FromPG for deletes, where a foreign key violation means the row is still in use.
func FromPGDelete(err error) error { return fromPG(err, true) }

func fromPG(err error, deleting bool) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pg *pgconn.PgError
	if !errors.As(err, &pg) {
		return err
	}
	switch pg.Code {
	case "23505":
		return Conflict("already exists")
	case "23503":
		if deleting {
			return Conflict("still in use")
		}
		return Invalid("a referenced object does not exist")
	case "23514", "23502", "22P02", "22001", "22003":
		return Invalid("value rejected by the database")
	}
	return err
}

// Page validates paging arguments; limit 0 means DefaultLimit.
func Page(limit, offset int) (int32, int32, error) {
	return PageMax(limit, offset, MaxLimit)
}

func PageMax(limit, offset, maxLimit int) (int32, int32, error) {
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 0 || limit > maxLimit || offset < 0 {
		return 0, 0, Invalid("limit must be 1..%d and offset >= 0", maxLimit)
	}
	return int32(limit), int32(offset), nil
}

// JSONObject returns raw if it is a JSON object, and {} when it is empty.
func JSONObject(field string, raw json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return json.RawMessage(`{}`), nil
	}
	if trimmed[0] != '{' || !json.Valid(trimmed) {
		return nil, Invalid("%s must be a JSON object", field)
	}
	return trimmed, nil
}

// Required trims s and rejects blank values.
func Required(field, s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", Invalid("%s is required", field)
	}
	return s, nil
}

// Blank turns a nil or whitespace-only string into nil.
func Blank(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return nil
	}
	return &t
}
