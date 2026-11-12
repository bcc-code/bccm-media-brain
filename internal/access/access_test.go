package access

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
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

func TestRequirePersonAdmin(t *testing.T) {
	ctx := context.Background()
	for name, c := range map[string]struct {
		p    *Principal
		want error
	}{
		"nobody":        {nil, domain.ErrUnauthenticated},
		"user":          {&Principal{UserID: "US-1"}, domain.ErrForbidden},
		"service":       {&Principal{UserID: "US-1", Service: true}, domain.ErrForbidden},
		"admin service": {&Principal{UserID: "US-1", Admin: true, Service: true, TokenID: "TK-1"}, domain.ErrForbidden},
		"admin":         {&Principal{UserID: "US-1", Admin: true}, nil},
		"system":        {ptr(System()), nil},
	} {
		c2 := ctx
		if c.p != nil {
			c2 = With(ctx, *c.p)
		}
		if err := RequirePersonAdmin(c2); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
}

func ptr[T any](v T) *T { return &v }

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("no entropy") }

func TestTokens(t *testing.T) {
	a, err := newToken(strings.NewReader(strings.Repeat("a", tokenBytes)))
	if err != nil || !IsServiceToken(a) || len(a) != len(TokenPrefix)+43 {
		t.Fatalf("token = %q, %v", a, err)
	}
	b, _ := newToken(strings.NewReader(strings.Repeat("b", tokenBytes)))
	if a == b || bytes.Equal(hashToken(a), hashToken(b)) || len(hashToken(a)) != 32 {
		t.Fatal("different randomness must give different tokens and hashes")
	}
	if strings.Contains(a, string(hashToken(a))) || !bytes.Equal(hashToken(a), hashToken(a)) {
		t.Fatal("hash")
	}
	if _, err := newToken(strings.NewReader("short")); err == nil {
		t.Fatal("too little randomness must fail")
	}
	if _, err := newToken(failingReader{}); err == nil {
		t.Fatal("a failing source must fail")
	}
	for _, s := range []string{"", "eyJhbGciOi.x.y", "MBS_abc", "Bearer mbs_abc"} {
		if IsServiceToken(s) {
			t.Errorf("%q is not a service token", s)
		}
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

func TestAudited(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(old)
	logged := func() int {
		n := strings.Count(buf.String(), AuditChange)
		buf.Reset()
		return n
	}
	ctx := With(context.Background(), Principal{UserID: "US-1", TokenID: "TK-1"})

	failure := errors.New("no")
	if err := Audited(ctx, failure, "a", "t"); err != failure || logged() != 0 {
		t.Fatalf("a failed write was recorded or its error lost: %v", err)
	}
	if err := Audited(ctx, nil, "a", "t", "k", "v"); err != nil || !strings.Contains(buf.String(), "user=US-1 token=TK-1 target=t k=v") || logged() != 1 {
		t.Fatalf("record = %q, %v", buf.String(), err)
	}
	_ = Audited(With(context.Background(), System()), nil, "a", "t")
	if !strings.Contains(buf.String(), "user=system target=t") || logged() != 1 {
		t.Fatalf("system record = %q", buf.String())
	}

	// Held records appear on commit, in order, and later ones are written straight away.
	held, release := HoldAudit(ctx)
	inner, releaseInner := HoldAudit(held)
	_ = Audited(held, nil, "first", "t")
	_ = Audited(inner, nil, "second", "t")
	releaseInner(false) // the outer hold decides
	if logged() != 0 {
		t.Fatal("held records were written before the release")
	}
	release(true)
	if i, j := strings.Index(buf.String(), "action=first"), strings.Index(buf.String(), "action=second"); i < 0 || j < i || logged() != 2 {
		t.Fatalf("released records = %q", buf.String())
	}
	release(true)
	_ = Audited(held, nil, "late", "t")
	if logged() != 1 {
		t.Fatal("a record after the release should be written once")
	}

	held, release = HoldAudit(ctx)
	_ = Audited(held, nil, "dropped", "t")
	release(false)
	_ = Audited(WithoutAudit(ctx), nil, "muted", "t")
	if logged() != 0 {
		t.Fatal("dropped or muted records were written")
	}
}

func TestOriginInAuditRecords(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(old)

	ctx := With(context.Background(), Principal{UserID: "US-1"})
	if OriginOf(ctx) != nil || OriginAttrs(ctx) != nil {
		t.Fatal("no origin outside a request")
	}
	_ = Audited(ctx, nil, "asset.update", "AS-1")
	if strings.Contains(buf.String(), "request=") || strings.Contains(buf.String(), "client=") {
		t.Fatalf("a change made outside a request names one: %s", buf.String())
	}

	buf.Reset()
	ctx = WithOrigin(ctx, &Origin{RequestID: "abc123", Client: "203.0.113.9"})
	_ = Audited(ctx, nil, "asset.update", "AS-1", "title", true)
	if got := buf.String(); !strings.Contains(got, "target=AS-1 title=true request=abc123 client=203.0.113.9") {
		t.Fatalf("audit record = %s", got)
	}
	if got := OriginAttrs(WithOrigin(ctx, &Origin{})); len(got) != 0 {
		t.Fatalf("an empty origin gives %v", got)
	}
}
