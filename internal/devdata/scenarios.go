package devdata

import (
	"context"
	"fmt"

	"github.com/bcc-media/mediabrain/internal/dummy"
)

// registry lists every scenario shown in the debug UI and the seed CLI.
//
// When the data model changes, update this list: every entity should be
// reachable from at least one scenario, plus edge cases worth eyeballing.
// Prefer going through domain services so generated data respects invariants.
var registry = []Scenario{
	{
		Name:        "empty",
		Description: "Wipe all data (keeps schema and migration history).",
		AlwaysReset: true,
		Run: func(context.Context, Env, Values) (string, error) {
			return "all tables truncated", nil
		},
	},
	{
		// PLACEHOLDER: replace with real media scenarios.
		Name:        "dummies",
		Description: "N dummies with realistic fake names.",
		Params: []Param{
			{Name: "count", Label: "Count", Type: Int, Default: "20", Min: ptr(1), Max: ptr(10000)},
			{Name: "prefix", Label: "Name prefix", Type: String, Help: "Optional prefix for every name"},
		},
		Run: func(ctx context.Context, env Env, v Values) (string, error) {
			svc := dummy.NewService(env.Q)
			for range v.Int("count") {
				name := env.Fake.DummyName()
				if p := v.String("prefix"); p != "" {
					name = p + " " + name
				}
				if _, err := svc.Create(ctx, name); err != nil {
					return "", err
				}
			}
			return fmt.Sprintf("created %d dummies", v.Int("count")), nil
		},
	},
	{
		// PLACEHOLDER: replace with real media edge cases.
		Name:        "dummy-edge-cases",
		Description: "Awkward names: unicode, emoji, very long, HTML/SQL-looking, padded whitespace.",
		Run: func(ctx context.Context, env Env, _ Values) (string, error) {
			svc := dummy.NewService(env.Q)
			for _, name := range edgeCaseNames() {
				if _, err := svc.Create(ctx, name); err != nil {
					return "", err
				}
			}
			return fmt.Sprintf("created %d edge-case dummies", len(edgeCaseNames())), nil
		},
	},
}

func edgeCaseNames() []string {
	long := make([]byte, 1000)
	for i := range long {
		long[i] = 'a' + byte(i%26)
	}
	return []string{
		"Ærøskøbing – Ünïcødé ñame",
		"日本語のタイトル",
		"🎬📼 Emoji reel 🎞️",
		string(long),
		"<script>alert('xss')</script>",
		"'; DROP TABLE dummy; --",
		"   padded with spaces   ",
		"x",
	}
}
