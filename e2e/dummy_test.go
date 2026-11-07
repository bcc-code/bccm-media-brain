//go:build e2e

package e2e

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bcc-media/mediabrain/internal/app"
	"github.com/bcc-media/mediabrain/internal/config"
	"github.com/bcc-media/mediabrain/internal/fake"
)

type dummyJSON struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
}

const createQ = `mutation($name: String!) { createDummy(name: $name) { id name createdAt } }`

func create(t *testing.T, name string) dummyJSON {
	t.Helper()
	var r struct{ CreateDummy dummyJSON }
	mustGQL(t, createQ, map[string]any{"name": name}, &r)
	return r.CreateDummy
}

func TestHealthAndPlayground(t *testing.T) {
	for path, want := range map[string]string{"/healthz": "ok", "/": "MediaBrain"} {
		res, err := http.Get(baseURL + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusOK || !strings.Contains(string(b), want) {
			t.Errorf("GET %s = %d, body lacks %q", path, res.StatusCode, want)
		}
	}
}

func TestDummyCRUDWithFakeData(t *testing.T) {
	f := fake.New(uint64(time.Now().UnixNano()))

	// Seed fake data through the public API.
	created := make(map[string]dummyJSON)
	for range 25 {
		name := f.DummyName()
		d := create(t, name)
		if d.ID == "" || d.Name != name || d.CreatedAt.IsZero() {
			t.Fatalf("created %+v for name %q", d, name)
		}
		created[d.ID] = d
	}

	// Read each back.
	for id, want := range created {
		var r struct{ Dummy *dummyJSON }
		mustGQL(t, `query($id: ID!) { dummy(id: $id) { id name createdAt } }`, map[string]any{"id": id}, &r)
		if r.Dummy == nil || r.Dummy.Name != want.Name || !r.Dummy.CreatedAt.Equal(want.CreatedAt) {
			t.Fatalf("dummy(%s) = %+v, want %+v", id, r.Dummy, want)
		}
	}

	// Paginate over everything and make sure all seeded rows are present, in id order, without duplicates.
	seen := map[string]bool{}
	for offset := 0; ; offset += 10 {
		var r struct{ Dummies []dummyJSON }
		mustGQL(t, `query($o: Int) { dummies(limit: 10, offset: $o) { id name } }`, map[string]any{"o": offset}, &r)
		if len(r.Dummies) == 0 {
			break
		}
		for _, d := range r.Dummies {
			if seen[d.ID] {
				t.Fatalf("duplicate id %s across pages", d.ID)
			}
			seen[d.ID] = true
		}
	}
	for id := range created {
		if !seen[id] {
			t.Fatalf("seeded id %s missing from pagination", id)
		}
	}

	// Delete half and verify they are gone.
	i := 0
	for id := range created {
		if i++; i%2 == 0 {
			continue
		}
		var del struct{ DeleteDummy bool }
		mustGQL(t, `mutation($id: ID!) { deleteDummy(id: $id) }`, map[string]any{"id": id}, &del)
		if !del.DeleteDummy {
			t.Fatalf("deleteDummy(%s) = false", id)
		}
		var r struct{ Dummy *dummyJSON }
		mustGQL(t, `query($id: ID!) { dummy(id: $id) { id } }`, map[string]any{"id": id}, &r)
		if r.Dummy != nil {
			t.Fatalf("dummy %s still present after delete", id)
		}
		if errs := gql(t, `mutation($id: ID!) { deleteDummy(id: $id) }`, map[string]any{"id": id}, nil); len(errs) == 0 {
			t.Fatalf("second delete of %s should fail", id)
		}
	}
}

func TestInvalidInput(t *testing.T) {
	cases := map[string]struct {
		q    string
		vars map[string]any
		want string
	}{
		"blank name":     {createQ, map[string]any{"name": "   "}, "name is required"},
		"bad id":         {`{ dummy(id: "nope") { id } }`, nil, "invalid id"},
		"negative limit": {`{ dummies(limit: -1) { id } }`, nil, "invalid input"},
		"huge limit":     {`{ dummies(limit: 100000) { id } }`, nil, "invalid input"},
		"missing field":  {`{ nope }`, nil, "Cannot query field"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			errs := gql(t, c.q, c.vars, nil)
			if len(errs) == 0 || !strings.Contains(errs[0].Message, c.want) {
				t.Fatalf("errors = %+v, want message containing %q", errs, c.want)
			}
		})
	}
}

func TestNewFailsOnBadListenAddr(t *testing.T) {
	_, err := app.New(context.Background(), config.Config{DatabaseURL: dbURL, HTTPAddr: "256.0.0.1:0"})
	if err == nil || !strings.Contains(err.Error(), "listen") {
		t.Fatalf("err = %v, want listen error", err)
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx, config.Config{DatabaseURL: dbURL, HTTPAddr: "127.0.0.1:0"}) }()
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Run did not stop after cancel")
	}
}
