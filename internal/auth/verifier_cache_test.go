package auth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bcc-media/mediabrain/internal/auth/authtest"
)

// jwks stands in front of an issuer's key set so a test can withdraw the keys, make the
// endpoint fail or hold a request, and count the fetches.
type jwks struct {
	*httptest.Server
	mode  atomic.Value // "ok" | "empty" | "down"
	hits  atomic.Int32
	hit   chan struct{} // receives once per request when set
	block chan struct{} // requests wait for it to close when set
}

func newJWKS(iss *authtest.Issuer) *jwks {
	j := &jwks{}
	j.mode.Store("ok")
	j.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		j.hits.Add(1)
		if j.hit != nil {
			j.hit <- struct{}{}
		}
		if j.block != nil {
			<-j.block
		}
		switch j.mode.Load() {
		case "empty":
			_, _ = w.Write([]byte(`{"keys":[]}`))
		case "down":
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			res, err := http.Get(iss.JWKSURL)
			if err != nil {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			defer res.Body.Close()
			_, _ = io.Copy(w, res.Body)
		}
	}))
	return j
}

// clock is a time source a test moves by hand.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// keyCache is a verifier on a hand-moved clock, the key endpoint it reads, and a function
// that signs a token valid at the clock's time.
func keyCache(t *testing.T) (*JWKSVerifier, *jwks, *clock, func() string) {
	t.Helper()
	iss := authtest.NewIssuer()
	t.Cleanup(iss.Close)
	j := newJWKS(iss)
	t.Cleanup(j.Close)
	c := &clock{t: time.Now()}
	v := verifier(iss)
	v.JWKSURL, v.now = j.URL, c.now
	token := func() string {
		return iss.Sign(map[string]any{"iss": iss.URL, "aud": authtest.Audience, "sub": "s", "exp": c.now().Add(time.Hour).Unix()})
	}
	return v, j, c, token
}

func wantFetches(t *testing.T, j *jwks, want int32) {
	t.Helper()
	if n := j.hits.Load(); n != want {
		t.Fatalf("fetches = %d, want %d", n, want)
	}
}

func TestWithdrawnKeyIsRejectedAfterMaxAge(t *testing.T) {
	v, j, c, token := keyCache(t)
	ctx := context.Background()
	if _, err := v.Verify(ctx, token()); err != nil {
		t.Fatal(err)
	}
	j.mode.Store("empty")

	c.advance(jwksMaxAge - time.Second)
	if _, err := v.Verify(ctx, token()); err != nil {
		t.Fatalf("inside the max age: %v", err)
	}
	wantFetches(t, j, 1)

	c.advance(24 * time.Hour)
	if _, err := v.Verify(ctx, token()); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("withdrawn key with a day-old cache: err = %v", err)
	}
	wantFetches(t, j, 2)
}

func TestCachedKeysOutliveAFailedRefreshOnlyForAWhile(t *testing.T) {
	v, j, c, token := keyCache(t)
	ctx := context.Background()
	if _, err := v.Verify(ctx, token()); err != nil {
		t.Fatal(err)
	}
	j.mode.Store("down")

	c.advance(jwksMaxAge)
	for range 3 {
		if _, err := v.Verify(ctx, token()); err != nil {
			t.Fatalf("refresh failed inside the limit: %v", err)
		}
	}
	wantFetches(t, j, 2) // failed attempts are spaced too
	c.advance(minKeyRefetch)
	if _, err := v.Verify(ctx, token()); err != nil {
		t.Fatal(err)
	}
	wantFetches(t, j, 3)

	c.advance(jwksMaxStale)
	for _, when := range []string{"on the attempt", "between attempts"} {
		if _, err := v.Verify(ctx, token()); err == nil || errors.Is(err, ErrInvalidToken) {
			t.Fatalf("past the limit, %s: err = %v, want the fetch error", when, err)
		}
	}
	wantFetches(t, j, 4)

	j.mode.Store("ok")
	c.advance(minKeyRefetch)
	if _, err := v.Verify(ctx, token()); err != nil {
		t.Fatalf("after the endpoint recovered: %v", err)
	}
}

func TestUnknownKeyRefetchIsSpaced(t *testing.T) {
	v, j, c, token := keyCache(t)
	ctx := context.Background()
	j.mode.Store("empty")
	for range 3 {
		if _, err := v.Verify(ctx, token()); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("unknown key: err = %v", err)
		}
	}
	wantFetches(t, j, 1)

	// A key that rotates in is picked up after the spacing, long before the max age.
	j.mode.Store("ok")
	c.advance(minKeyRefetch)
	if _, err := v.Verify(ctx, token()); err != nil {
		t.Fatalf("new key: %v", err)
	}
	wantFetches(t, j, 2)
}

func TestConcurrentVerifiesShareOneFetch(t *testing.T) {
	v, j, _, token := keyCache(t)
	j.hit, j.block = make(chan struct{}, 100), make(chan struct{})
	const callers = 20
	errs := make(chan error, callers)
	for range callers {
		go func() {
			_, err := v.Verify(context.Background(), token())
			errs <- err
		}()
	}
	<-j.hit

	// A caller that gives up while waiting neither blocks nor fails the others.
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := v.Verify(gone, token()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled waiter: err = %v", err)
	}

	time.Sleep(50 * time.Millisecond) // let the others reach the wait
	close(j.block)
	for range callers {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
	wantFetches(t, j, 1)
}
