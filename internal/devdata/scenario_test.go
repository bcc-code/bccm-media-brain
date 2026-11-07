package devdata

import (
	"context"
	"errors"
	"testing"

	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/fake"
)

func TestResolve(t *testing.T) {
	s := Scenario{Params: []Param{
		{Name: "n", Type: Int, Default: "5", Min: ptr(1), Max: ptr(10)},
		{Name: "s", Type: String, Default: "def"},
		{Name: "b", Type: Bool, Default: "false"},
	}}
	v, err := s.Resolve(nil)
	if err != nil || v.Int("n") != 5 || v.String("s") != "def" || v.Bool("b") {
		t.Fatalf("defaults = %v, %v", v, err)
	}
	v, err = s.Resolve(map[string]string{"n": "10", "s": "x", "b": "true"})
	if err != nil || v.Int("n") != 10 || v.String("s") != "x" || !v.Bool("b") {
		t.Fatalf("explicit = %v, %v", v, err)
	}
	for name, raw := range map[string]map[string]string{
		"not int":   {"n": "abc"},
		"too small": {"n": "0"},
		"too big":   {"n": "11"},
		"bad bool":  {"b": "maybe"},
		"unknown":   {"zzz": "1"},
	} {
		if _, err := s.Resolve(raw); !errors.Is(err, ErrInvalidParams) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestFind(t *testing.T) {
	for _, s := range Scenarios() {
		got, err := Find(s.Name)
		if err != nil || got.Name != s.Name {
			t.Fatalf("Find(%q) = %v, %v", s.Name, got.Name, err)
		}
		if s.Description == "" || s.Run == nil {
			t.Errorf("scenario %q needs a description and Run", s.Name)
		}
		// Every scenario's defaults must be valid.
		if _, err := s.Resolve(nil); err != nil {
			t.Errorf("scenario %q defaults invalid: %v", s.Name, err)
		}
	}
	if _, err := Find("nope"); !errors.Is(err, ErrUnknownScenario) {
		t.Fatalf("err = %v", err)
	}
}

// countingQuerier records created dummies; optionally fails.
type countingQuerier struct {
	sqlc.Querier
	names []string
	err   error
}

func (q *countingQuerier) CreateDummy(_ context.Context, name string) (sqlc.Dummy, error) {
	if q.err != nil {
		return sqlc.Dummy{}, q.err
	}
	q.names = append(q.names, name)
	return sqlc.Dummy{ID: int64(len(q.names)), Name: name}, nil
}

// TestScenariosRun exercises every scenario's Run against an in-memory querier,
// so new scenarios get basic coverage automatically.
func TestScenariosRun(t *testing.T) {
	ctx := context.Background()
	for _, s := range Scenarios() {
		v, _ := s.Resolve(nil)
		q := &countingQuerier{}
		summary, err := s.Run(ctx, Env{Q: q, Fake: fake.New(1)}, v)
		if err != nil || summary == "" {
			t.Errorf("%s: summary %q, err %v", s.Name, summary, err)
		}
		if !s.AlwaysReset && len(q.names) == 0 {
			t.Errorf("%s created nothing", s.Name)
		}
		q.err = errors.New("boom")
		if _, err := s.Run(ctx, Env{Q: q, Fake: fake.New(1)}, v); err == nil && !s.AlwaysReset {
			t.Errorf("%s: expected error to propagate", s.Name)
		}
	}
}

func TestDummiesPrefix(t *testing.T) {
	s, _ := Find("dummies")
	v, _ := s.Resolve(map[string]string{"count": "3", "prefix": "TEST"})
	q := &countingQuerier{}
	if _, err := s.Run(context.Background(), Env{Q: q, Fake: fake.New(1)}, v); err != nil {
		t.Fatal(err)
	}
	if len(q.names) != 3 {
		t.Fatalf("created %d", len(q.names))
	}
	for _, n := range q.names {
		if n[:5] != "TEST " {
			t.Errorf("name %q lacks prefix", n)
		}
	}
}
