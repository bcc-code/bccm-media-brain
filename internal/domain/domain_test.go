package domain

import (
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestFromPG(t *testing.T) {
	boom := errors.New("boom")
	pg := func(code string) error { return fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: code}) }
	cases := []struct {
		in       error
		want     error
		deleting bool
	}{
		{nil, nil, false},
		{pgx.ErrNoRows, ErrNotFound, false},
		{pg("23505"), ErrConflict, false},
		{pg("23503"), ErrInvalidInput, false},
		{pg("23503"), ErrConflict, true},
		{pg("23514"), ErrInvalidInput, false},
		{pg("22P02"), ErrInvalidInput, true},
		{boom, boom, false},
	}
	for _, c := range cases {
		got := FromPG(c.in)
		if c.deleting {
			got = FromPGDelete(c.in)
		}
		if !errors.Is(got, c.want) {
			t.Errorf("%v (deleting %v) = %v, want %v", c.in, c.deleting, got, c.want)
		}
	}
	if err := FromPG(pg("57014")); errors.Is(err, ErrInvalidInput) || errors.Is(err, ErrConflict) || err == nil {
		t.Errorf("unknown code must stay an internal error, got %v", err)
	}
}

func TestPage(t *testing.T) {
	if l, o, err := Page(0, 0); err != nil || l != DefaultLimit || o != 0 {
		t.Fatalf("defaults = %d %d %v", l, o, err)
	}
	if l, o, err := Page(MaxLimit, 7); err != nil || l != MaxLimit || o != 7 {
		t.Fatalf("max = %d %d %v", l, o, err)
	}
	for _, c := range [][2]int{{-1, 0}, {MaxLimit + 1, 0}, {1, -1}} {
		if _, _, err := Page(c[0], c[1]); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("Page(%v) err = %v", c, err)
		}
	}
	if _, _, err := PageMax(5000, 0, 5000); err != nil {
		t.Fatal(err)
	}
}

func TestJSONObject(t *testing.T) {
	for _, in := range []string{"", "  ", "null"} {
		if got, err := JSONObject("m", []byte(in)); err != nil || string(got) != "{}" {
			t.Errorf("%q = %s, %v", in, got, err)
		}
	}
	if got, err := JSONObject("m", []byte(` {"a":1} `)); err != nil || string(got) != `{"a":1}` {
		t.Fatalf("object = %s, %v", got, err)
	}
	for _, in := range []string{"[]", `"x"`, "1", `{"a":`} {
		if _, err := JSONObject("m", []byte(in)); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%q err = %v", in, err)
		}
	}
}

func TestRequiredAndBlank(t *testing.T) {
	if s, err := Required("name", "  x  "); err != nil || s != "x" {
		t.Fatalf("%q %v", s, err)
	}
	if _, err := Required("name", "   "); !errors.Is(err, ErrInvalidInput) {
		t.Fatal(err)
	}
	blank, text := "  ", " a "
	if Blank(nil) != nil || Blank(&blank) != nil || *Blank(&text) != "a" {
		t.Fatal("Blank")
	}
	if !errors.Is(Conflict("x %d", 1), ErrConflict) || !errors.Is(Invalid("x"), ErrInvalidInput) {
		t.Fatal("constructors")
	}
}

func TestExpectedRevision(t *testing.T) {
	if r, err := ExpectedRevision(nil); r != nil || err != nil {
		t.Fatalf("no expectation = %v, %v", r, err)
	}
	seven := 7
	if r, err := ExpectedRevision(&seven); err != nil || r == nil || *r != 7 {
		t.Fatalf("7 = %v, %v", r, err)
	}
	for _, bad := range []int{0, -3, math.MaxInt32 + 1} {
		if _, err := ExpectedRevision(&bad); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%d: err = %v", bad, err)
		}
	}
}

func TestFromPGUpdate(t *testing.T) {
	boom := errors.New("boom")
	expected := int32(3)
	never := func() (int32, error) { t.Fatal("the row should not be read again"); return 0, nil }
	at := func(rev int32, err error) func() (int32, error) {
		return func() (int32, error) { return rev, err }
	}
	cases := map[string]struct {
		err      error
		expected *int32
		current  func() (int32, error)
		want     error
	}{
		"updated":                         {nil, &expected, never, nil},
		"no expectation, no row":          {pgx.ErrNoRows, nil, never, ErrNotFound},
		"another error":                   {boom, &expected, never, boom},
		"changed by someone else":         {pgx.ErrNoRows, &expected, at(5, nil), ErrConflict},
		"gone":                            {pgx.ErrNoRows, &expected, at(0, pgx.ErrNoRows), ErrNotFound},
		"reading the row again fails too": {pgx.ErrNoRows, &expected, at(0, boom), boom},
	}
	for name, c := range cases {
		if got := FromPGUpdate(c.err, c.expected, c.current); !errors.Is(got, c.want) || (c.want == nil && got != nil) {
			t.Errorf("%s: got %v, want %v", name, got, c.want)
		}
	}
}

func TestDetail(t *testing.T) {
	fk := func(table, constraint string) error {
		return FromPG(&pgconn.PgError{Code: "23503", TableName: table, ConstraintName: constraint})
	}
	cases := map[string]struct {
		err  error
		kind error
		want Detail
	}{
		"plain invalid":     {Invalid("no"), ErrInvalidInput, Detail{}},
		"plain conflict":    {Conflict("no"), ErrConflict, Detail{}},
		"field":             {InvalidField("code", "code must be %d letters", 4), ErrInvalidInput, Detail{Field: "code"}},
		"required":          {second(Required("name", " ")), ErrInvalidInput, Detail{Field: "name"}},
		"exists":            {FromPG(&pgconn.PgError{Code: "23505"}), ErrConflict, Detail{Reason: ReasonAlreadyExists}},
		"in use":            {FromPGDelete(&pgconn.PgError{Code: "23503"}), ErrConflict, Detail{Reason: ReasonInUse}},
		"stale":             {Stale(7, "now at %d", 7), ErrConflict, Detail{Reason: ReasonStaleRevision, CurrentRevision: 7}},
		"reference":         {fk("asset", "asset_type_id_fkey"), ErrInvalidInput, Detail{Field: "typeId"}},
		"one word":          {fk("subtitle_track", "subtitle_track_language_fkey"), ErrInvalidInput, Detail{Field: "language"}},
		"named differently": {fk("asset", "fk_type"), ErrInvalidInput, Detail{}},
		"not a foreign key": {fk("asset", "asset_title_check"), ErrInvalidInput, Detail{}},
		"wrapped":           {fmt.Errorf("while saving: %w", Exists("taken")), ErrConflict, Detail{Reason: ReasonAlreadyExists}},
		"something else":    {errors.New("boom"), nil, Detail{}},
	}
	for name, c := range cases {
		if got := DetailOf(c.err); got != c.want || (c.kind != nil && !errors.Is(c.err, c.kind)) {
			t.Errorf("%s: %v gives %+v, want %+v of kind %v", name, c.err, got, c.want, c.kind)
		}
	}
	if got := InvalidField("code", "code must be %d letters", 4).Error(); got != "invalid input: code must be 4 letters" {
		t.Errorf("message = %q", got)
	}
}

func second[T any](_ T, err error) error { return err }
