package dummy

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/fake"
)

var errBoom = errors.New("boom")

// fakeQuerier is an in-memory sqlc.Querier.
type fakeQuerier struct {
	rows       map[int64]sqlc.Dummy
	nextID     int64
	err        error
	lastParams sqlc.ListDummiesParams
}

func newFakeQuerier(seed []sqlc.Dummy) *fakeQuerier {
	q := &fakeQuerier{rows: map[int64]sqlc.Dummy{}}
	for _, d := range seed {
		q.rows[d.ID] = d
		q.nextID = max(q.nextID, d.ID)
	}
	return q
}

func (q *fakeQuerier) GetDummy(_ context.Context, id int64) (sqlc.Dummy, error) {
	if q.err != nil {
		return sqlc.Dummy{}, q.err
	}
	d, ok := q.rows[id]
	if !ok {
		return sqlc.Dummy{}, pgx.ErrNoRows
	}
	return d, nil
}

func (q *fakeQuerier) ListDummies(_ context.Context, p sqlc.ListDummiesParams) ([]sqlc.Dummy, error) {
	q.lastParams = p
	if q.err != nil {
		return nil, q.err
	}
	var out []sqlc.Dummy
	for id := int64(1); id <= q.nextID; id++ {
		if d, ok := q.rows[id]; ok {
			out = append(out, d)
		}
	}
	lo := min(int(p.Offset), len(out))
	hi := min(lo+int(p.Limit), len(out))
	return out[lo:hi], nil
}

func (q *fakeQuerier) CreateDummy(_ context.Context, name string) (sqlc.Dummy, error) {
	if q.err != nil {
		return sqlc.Dummy{}, q.err
	}
	q.nextID++
	d := sqlc.Dummy{ID: q.nextID, Name: name}
	q.rows[d.ID] = d
	return d, nil
}

func (q *fakeQuerier) DeleteDummy(_ context.Context, id int64) (int64, error) {
	if q.err != nil {
		return 0, q.err
	}
	if _, ok := q.rows[id]; !ok {
		return 0, nil
	}
	delete(q.rows, id)
	return 1, nil
}

func TestGet(t *testing.T) {
	ctx := context.Background()
	seed := fake.New(1).Dummies(3)
	q := newFakeQuerier(seed)
	s := NewService(q)

	got, err := s.Get(ctx, 2)
	if err != nil || got != seed[1] {
		t.Fatalf("Get(2) = %+v, %v", got, err)
	}
	if _, err := s.Get(ctx, 99); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(99) err = %v, want ErrNotFound", err)
	}
	q.err = errBoom
	if _, err := s.Get(ctx, 1); !errors.Is(err, errBoom) {
		t.Fatalf("Get err = %v, want wrapped errBoom", err)
	}
}

func TestList(t *testing.T) {
	ctx := context.Background()
	q := newFakeQuerier(fake.New(2).Dummies(10))
	s := NewService(q)

	got, err := s.List(ctx, 0, 0)
	if err != nil || len(got) != 10 || q.lastParams.Limit != DefaultLimit {
		t.Fatalf("List default = %d items, params %+v, err %v", len(got), q.lastParams, err)
	}
	got, err = s.List(ctx, 3, 8)
	if err != nil || len(got) != 2 || got[0].ID != 9 {
		t.Fatalf("List(3,8) = %+v, %v", got, err)
	}
	for _, c := range []struct{ limit, offset int }{{-1, 0}, {MaxLimit + 1, 0}, {10, -1}} {
		if _, err := s.List(ctx, c.limit, c.offset); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("List(%d,%d) err = %v, want ErrInvalidInput", c.limit, c.offset, err)
		}
	}
	q.err = errBoom
	if _, err := s.List(ctx, 1, 0); !errors.Is(err, errBoom) {
		t.Fatalf("List err = %v, want errBoom", err)
	}
}

func TestCreate(t *testing.T) {
	ctx := context.Background()
	q := newFakeQuerier(nil)
	s := NewService(q)

	got, err := s.Create(ctx, "  Hello  ")
	if err != nil || got.Name != "Hello" || got.ID != 1 {
		t.Fatalf("Create = %+v, %v", got, err)
	}
	if _, err := s.Create(ctx, "   "); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Create blank err = %v", err)
	}
	q.err = errBoom
	if _, err := s.Create(ctx, "x"); !errors.Is(err, errBoom) {
		t.Fatalf("Create err = %v", err)
	}
}

func TestDelete(t *testing.T) {
	ctx := context.Background()
	q := newFakeQuerier(fake.New(3).Dummies(2))
	s := NewService(q)

	if err := s.Delete(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second Delete err = %v", err)
	}
	q.err = errBoom
	if err := s.Delete(ctx, 2); !errors.Is(err, errBoom) {
		t.Fatalf("Delete err = %v", err)
	}
}
