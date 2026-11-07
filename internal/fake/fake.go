// Package fake builds realistic, seedable fake data for tests and dev scenarios.
package fake

import (
	"time"

	"github.com/brianvoe/gofakeit/v7"

	"github.com/bcc-media/mediabrain/internal/db/sqlc"
)

// epoch anchors generated timestamps so a given seed is fully reproducible.
var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// Faker wraps gofakeit with a fixed seed so test data is reproducible.
type Faker struct{ f *gofakeit.Faker }

// New returns a Faker seeded with seed.
func New(seed uint64) *Faker { return &Faker{f: gofakeit.New(seed)} }

// DummyName returns a plausible media-ish title.
func (x *Faker) DummyName() string {
	return x.f.MovieName() + " - " + x.f.Noun()
}

// Dummy returns a dummy row with the given id.
func (x *Faker) Dummy(id int64) sqlc.Dummy {
	return sqlc.Dummy{
		ID:        id,
		Name:      x.DummyName(),
		CreatedAt: x.f.DateRange(epoch.AddDate(-1, 0, 0), epoch).UTC(),
	}
}

// Dummies returns n dummies with ids 1..n.
func (x *Faker) Dummies(n int) []sqlc.Dummy {
	out := make([]sqlc.Dummy, n)
	for i := range out {
		out[i] = x.Dummy(int64(i + 1))
	}
	return out
}
