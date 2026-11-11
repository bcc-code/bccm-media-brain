// Package domain holds the errors and input rules shared by all domain services.
package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
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
	// ErrTimeout means the request ran past its deadline and was stopped. Unlike ErrUnavailable,
	// trying the same request again will not help: it has to ask for less.
	ErrTimeout = errors.New("the request took too long")
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

// ExpectedRevision validates the revision an update says it is based on. Nil means the caller
// did not say, and the update overwrites whatever is there.
func ExpectedRevision(expected *int) (*int32, error) {
	if expected == nil {
		return nil, nil
	}
	if *expected < 1 || *expected > math.MaxInt32 {
		return nil, Invalid("expectedRevision must be a revision number")
	}
	r := int32(*expected)
	return &r, nil
}

// FromPGUpdate is FromPG for an UPDATE that only matches the expected revision. Such an update
// finds no row both when the row is gone and when someone else changed it first; current reads
// the row's revision now to tell the two apart.
func FromPGUpdate(err error, expected *int32, current func() (int32, error)) error {
	if expected == nil || !errors.Is(err, pgx.ErrNoRows) {
		return FromPG(err)
	}
	now, err := current()
	if err != nil {
		return FromPG(err)
	}
	return Conflict("changed since revision %d; it is now at revision %d", *expected, now)
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
