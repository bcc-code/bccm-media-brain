// Package devdata generates development data from named, parameterised
// scenarios. Scenarios are exposed through the debug UI (/debug/, DEBUG=true)
// and the `mediabrain seed` CLI.
//
// KEEP THIS IN SYNC WITH THE DATA MODEL: when tables or domain services are
// added or changed, add/update scenarios in scenarios.go (see AGENTS.md).
package devdata

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/fake"
)

// ErrInvalidParams is returned when scenario parameters fail validation.
var ErrInvalidParams = errors.New("devdata: invalid params")

// ErrUnknownScenario is returned for a scenario name that is not registered.
var ErrUnknownScenario = errors.New("devdata: unknown scenario")

// ParamType is the type of a scenario parameter.
type ParamType string

const (
	Int    ParamType = "int"
	String ParamType = "string"
	Bool   ParamType = "bool"
)

// Param describes one scenario input; the debug UI renders a field per Param.
type Param struct {
	Name    string    `json:"name"`
	Label   string    `json:"label"`
	Type    ParamType `json:"type"`
	Default string    `json:"default"`
	Help    string    `json:"help,omitempty"`
	Min     *int      `json:"min,omitempty"` // Int only
	Max     *int      `json:"max,omitempty"` // Int only
}

// Env is what a scenario gets to work with. Q is scoped to the scenario's
// transaction, so a failing scenario leaves no partial data behind.
type Env struct {
	Q    sqlc.Querier
	Fake *fake.Faker
}

// Scenario is a named recipe that produces a particular data situation.
type Scenario struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Params      []Param `json:"params"`
	// AlwaysReset forces a wipe of all data before Run (e.g. "empty").
	AlwaysReset bool `json:"alwaysReset"`
	// Run creates the data and returns a short human-readable summary.
	Run func(ctx context.Context, env Env, v Values) (string, error) `json:"-"`
}

// Values are validated, typed parameter values.
type Values map[string]any

// Int returns an int parameter.
func (v Values) Int(name string) int { i, _ := v[name].(int); return i }

// String returns a string parameter.
func (v Values) String(name string) string { s, _ := v[name].(string); return s }

// Bool returns a bool parameter.
func (v Values) Bool(name string) bool { b, _ := v[name].(bool); return b }

// Resolve validates raw string params against the scenario's spec, applying defaults.
func (s Scenario) Resolve(raw map[string]string) (Values, error) {
	known := make(map[string]bool, len(s.Params))
	out := Values{}
	for _, p := range s.Params {
		known[p.Name] = true
		str, ok := raw[p.Name]
		if !ok || str == "" {
			str = p.Default
		}
		switch p.Type {
		case Int:
			i, err := strconv.Atoi(str)
			if err != nil {
				return nil, fmt.Errorf("%w: %s must be an integer", ErrInvalidParams, p.Name)
			}
			if (p.Min != nil && i < *p.Min) || (p.Max != nil && i > *p.Max) {
				return nil, fmt.Errorf("%w: %s out of range", ErrInvalidParams, p.Name)
			}
			out[p.Name] = i
		case Bool:
			b, err := strconv.ParseBool(str)
			if err != nil {
				return nil, fmt.Errorf("%w: %s must be a boolean", ErrInvalidParams, p.Name)
			}
			out[p.Name] = b
		default:
			out[p.Name] = str
		}
	}
	for k := range raw {
		if !known[k] {
			return nil, fmt.Errorf("%w: unknown param %q", ErrInvalidParams, k)
		}
	}
	return out, nil
}

// Scenarios returns all registered scenarios in display order.
func Scenarios() []Scenario { return registry }

// Find returns the scenario with the given name.
func Find(name string) (Scenario, error) {
	for _, s := range registry {
		if s.Name == name {
			return s, nil
		}
	}
	return Scenario{}, fmt.Errorf("%w: %q", ErrUnknownScenario, name)
}

func ptr(i int) *int { return &i }
