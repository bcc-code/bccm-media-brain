package graph_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/99designs/gqlgen/graphql/handler"

	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/dummy"
	"github.com/bcc-media/mediabrain/internal/fake"
	"github.com/bcc-media/mediabrain/internal/graph"
	"github.com/bcc-media/mediabrain/internal/graph/generated"
)

// fakeService is a scriptable graph.DummyService.
type fakeService struct {
	rows []sqlc.Dummy
	err  error

	gotLimit, gotOffset int
}

func (f *fakeService) Get(_ context.Context, id int64) (sqlc.Dummy, error) {
	if f.err != nil {
		return sqlc.Dummy{}, f.err
	}
	for _, d := range f.rows {
		if d.ID == id {
			return d, nil
		}
	}
	return sqlc.Dummy{}, dummy.ErrNotFound
}

func (f *fakeService) List(_ context.Context, limit, offset int) ([]sqlc.Dummy, error) {
	f.gotLimit, f.gotOffset = limit, offset
	return f.rows, f.err
}

func (f *fakeService) Create(_ context.Context, name string) (sqlc.Dummy, error) {
	if f.err != nil {
		return sqlc.Dummy{}, f.err
	}
	d := sqlc.Dummy{ID: int64(len(f.rows) + 1), Name: name}
	f.rows = append(f.rows, d)
	return d, nil
}

func (f *fakeService) Delete(ctx context.Context, id int64) error {
	_, err := f.Get(ctx, id)
	return err
}

func newClient(svc graph.DummyService) *client.Client {
	srv := handler.NewDefaultServer(generated.NewExecutableSchema(generated.Config{
		Resolvers: &graph.Resolver{DummySvc: svc},
	}))
	return client.New(srv)
}

type dummyJSON struct {
	ID        string
	Name      string
	CreatedAt string
}

func TestPing(t *testing.T) {
	var resp struct{ Ping string }
	newClient(&fakeService{}).MustPost(`{ ping }`, &resp)
	if resp.Ping != "pong" {
		t.Fatalf("ping = %q", resp.Ping)
	}
}

func TestDummyQuery(t *testing.T) {
	rows := fake.New(7).Dummies(2)
	c := newClient(&fakeService{rows: rows})

	var resp struct{ Dummy *dummyJSON }
	c.MustPost(`query($id: ID!) { dummy(id: $id) { id name createdAt } }`, &resp, client.Var("id", "2"))
	if resp.Dummy == nil || resp.Dummy.ID != "2" || resp.Dummy.Name != rows[1].Name || resp.Dummy.CreatedAt == "" {
		t.Fatalf("dummy = %+v", resp.Dummy)
	}

	resp.Dummy = nil
	c.MustPost(`{ dummy(id: "99") { id } }`, &resp)
	if resp.Dummy != nil {
		t.Fatalf("missing dummy should be null, got %+v", resp.Dummy)
	}

	for _, id := range []string{"abc", "0", "-1"} {
		if err := c.Post(`query($id: ID!) { dummy(id: $id) { id } }`, &resp, client.Var("id", id)); err == nil ||
			!strings.Contains(err.Error(), "invalid id") {
			t.Errorf("id %q: err = %v", id, err)
		}
	}

	errC := newClient(&fakeService{err: errors.New("db down")})
	if err := errC.Post(`{ dummy(id: "1") { id } }`, &resp); err == nil {
		t.Fatal("expected error")
	}
}

func TestDummiesQuery(t *testing.T) {
	svc := &fakeService{rows: fake.New(8).Dummies(3)}
	c := newClient(svc)

	var resp struct{ Dummies []dummyJSON }
	c.MustPost(`{ dummies { id name } }`, &resp)
	if len(resp.Dummies) != 3 || svc.gotLimit != 50 || svc.gotOffset != 0 {
		t.Fatalf("dummies = %+v (limit %d offset %d)", resp.Dummies, svc.gotLimit, svc.gotOffset)
	}
	c.MustPost(`{ dummies(limit: null, offset: null) { id } }`, &resp)
	if svc.gotLimit != 0 || svc.gotOffset != 0 {
		t.Fatalf("null args: limit %d offset %d", svc.gotLimit, svc.gotOffset)
	}

	svc.err = errors.New("db down")
	if err := c.Post(`{ dummies { id } }`, &resp); err == nil {
		t.Fatal("expected error")
	}
}

func TestMutations(t *testing.T) {
	svc := &fakeService{}
	c := newClient(svc)

	var created struct{ CreateDummy dummyJSON }
	c.MustPost(`mutation { createDummy(name: "first") { id name } }`, &created)
	if created.CreateDummy.ID != "1" || created.CreateDummy.Name != "first" {
		t.Fatalf("created = %+v", created)
	}

	var deleted struct{ DeleteDummy bool }
	c.MustPost(`mutation { deleteDummy(id: "1") }`, &deleted)
	if !deleted.DeleteDummy {
		t.Fatal("deleteDummy returned false")
	}
	if err := c.Post(`mutation { deleteDummy(id: "x") }`, &deleted); err == nil {
		t.Fatal("expected invalid id error")
	}
	if err := c.Post(`mutation { deleteDummy(id: "42") }`, &deleted); err == nil {
		t.Fatal("expected not found error")
	}

	svc.err = errors.New("db down")
	if err := c.Post(`mutation { createDummy(name: "x") { id } }`, &created); err == nil {
		t.Fatal("expected error")
	}
}
