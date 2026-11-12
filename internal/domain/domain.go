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

// Reasons tell the kinds of conflict apart, so that a client does not have to read the message:
// reload and merge, mark a field as taken, or say what still uses the object.
const (
	ReasonStaleRevision = "STALE_REVISION"
	ReasonAlreadyExists = "ALREADY_EXISTS"
	ReasonInUse         = "IN_USE"
)

// Detail is what a client can act on without reading the message. Every part is optional.
type Detail struct {
	// Reason is one of the Reason constants, for a conflict.
	Reason string
	// Field is the input field or argument that is wrong, as the API names it.
	Field string
	// CurrentRevision is where the object is now, when Reason is ReasonStaleRevision.
	CurrentRevision int32
}

type detailed struct {
	kind error
	msg  string
	Detail
}

func (e *detailed) Error() string { return e.kind.Error() + ": " + e.msg }
func (e *detailed) Unwrap() error { return e.kind }

// DetailOf returns what err says beyond its code.
func DetailOf(err error) Detail {
	var e *detailed
	if errors.As(err, &e) {
		return e.Detail
	}
	return Detail{}
}

func Invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidInput, fmt.Sprintf(format, a...))
}

// InvalidField is Invalid for a problem with one input field or argument.
func InvalidField(field, format string, a ...any) error {
	return &detailed{kind: ErrInvalidInput, msg: fmt.Sprintf(format, a...), Detail: Detail{Field: field}}
}

func Conflict(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrConflict, fmt.Sprintf(format, a...))
}

// Exists is a Conflict because the object, or a value that must be unique, is already there.
func Exists(format string, a ...any) error {
	return &detailed{kind: ErrConflict, msg: fmt.Sprintf(format, a...), Detail: Detail{Reason: ReasonAlreadyExists}}
}

// Stale is a Conflict because the caller's change was based on a revision that is no longer
// the current one.
func Stale(current int32, format string, a ...any) error {
	return &detailed{kind: ErrConflict, msg: fmt.Sprintf(format, a...),
		Detail: Detail{Reason: ReasonStaleRevision, CurrentRevision: current}}
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
		return Exists("already exists")
	case "23503":
		if deleting {
			return &detailed{kind: ErrConflict, msg: "still in use", Detail: Detail{Reason: ReasonInUse}}
		}
		return InvalidField(referencingField(pg), "a referenced object does not exist")
	case "23514", "23502", "22P02", "22001", "22003":
		return Invalid("value rejected by the database")
	}
	return err
}

// referencingField names the input field behind a foreign key violation. Postgres names such a
// constraint <table>_<column>_fkey and the API names the field after the column, so
// asset_type_id_fkey on asset is typeId. A constraint named otherwise gives no field.
func referencingField(pg *pgconn.PgError) string {
	column, ok := strings.CutPrefix(pg.ConstraintName, pg.TableName+"_")
	if !ok {
		return ""
	}
	if column, ok = strings.CutSuffix(column, "_fkey"); !ok {
		return ""
	}
	parts := strings.Split(column, "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

// ExpectedRevision validates the revision an update says it is based on. Nil means the caller
// did not say, and the update overwrites whatever is there.
func ExpectedRevision(expected *int) (*int32, error) {
	if expected == nil {
		return nil, nil
	}
	if *expected < 1 || *expected > math.MaxInt32 {
		return nil, InvalidField("expectedRevision", "expectedRevision must be a revision number")
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
	return Stale(now, "changed since revision %d; it is now at revision %d", *expected, now)
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
		return nil, InvalidField(field, "%s must be a JSON object", field)
	}
	return trimmed, nil
}

// Required trims s and rejects blank values.
func Required(field, s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", InvalidField(field, "%s is required", field)
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
