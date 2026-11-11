package graph

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/bcc-media/mediabrain/internal/domain"
)

func TestMask(t *testing.T) {
	if err := mask(fmt.Errorf("query: %w", context.DeadlineExceeded)); !errors.Is(err, domain.ErrTimeout) {
		t.Fatalf("a statement stopped by the deadline = %v, want the timeout error", err)
	}
	if err := mask(context.Canceled); !errors.As(err, &internalError{}) {
		t.Fatalf("a request the client abandoned = %v, want it masked", err)
	}
	if err := mask(domain.Invalid("x")); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("a domain error = %v, want it kept", err)
	}
	if mask(nil) != nil {
		t.Fatal("no error must stay no error")
	}
}
