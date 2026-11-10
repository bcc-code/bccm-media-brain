package access

import (
	"context"
	"errors"
	"testing"

	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/domain"
)

func TestPrincipal(t *testing.T) {
	ctx := context.Background()
	if _, err := From(ctx); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("no principal: %v", err)
	}
	if err := RequireAdmin(ctx); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("no principal: %v", err)
	}
	user := Principal{UserID: "US-1", GroupIDs: []string{"UG-1", "UG-2"}}
	ctx = With(ctx, user)
	if got, err := From(ctx); err != nil || got.UserID != "US-1" {
		t.Fatalf("From = %+v, %v", got, err)
	}
	if ids := user.IDs(); len(ids) != 3 || ids[0] != "US-1" || *user.UserRef() != "US-1" {
		t.Fatalf("ids = %v", ids)
	}
	if err := RequireAdmin(ctx); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("non-admin: %v", err)
	}
	sys := System()
	if len(sys.IDs()) != 0 || sys.UserRef() != nil || RequireAdmin(With(ctx, sys)) != nil {
		t.Fatal("system principal")
	}
}

func TestLevels(t *testing.T) {
	levels := []sqlc.PermissionLevel{None, View, Download, Edit, Delete, Admin}
	for i, have := range levels {
		for j, need := range levels {
			if j == 0 {
				continue
			}
			if got, want := Allows(have, need), i >= j; got != want {
				t.Errorf("Allows(%q, %q) = %v", have, need, got)
			}
		}
	}
	if err := Decide(None, View); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("no access must look like not found, got %v", err)
	}
	if err := Decide(View, Edit); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("too little access: %v", err)
	}
	if err := Decide(Edit, Edit); err != nil {
		t.Errorf("enough access: %v", err)
	}
	if higher(View, Edit) != Edit || higher(Admin, Edit) != Admin || higher(None, None) != None {
		t.Error("higher")
	}
}
