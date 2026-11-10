package devdata

import (
	"errors"
	"testing"
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
		if _, err := s.Resolve(nil); err != nil {
			t.Errorf("scenario %q defaults invalid: %v", s.Name, err)
		}
	}
	if _, err := Find("nope"); !errors.Is(err, ErrUnknownScenario) {
		t.Fatalf("err = %v", err)
	}
}
