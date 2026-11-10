package domain

import (
	"errors"
	"fmt"
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
