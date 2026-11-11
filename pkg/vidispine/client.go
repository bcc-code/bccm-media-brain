// Package vidispine is a client for the Vidispine REST API, which Cantemo Portal exposes under
// /API. It depends on the standard library only, so any Go project can import it.
//
// It covers reading: item metadata, shapes and their files, storages and collections.
package vidispine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultTimeout = 30 * time.Second
	defaultRetries = 3
	bodyLimit      = 64 << 20
	errorBodyLimit = 2 << 10
)

// ErrNotFound matches (with errors.Is) every error for a 404 response.
var ErrNotFound = errors.New("vidispine: not found")

// ErrShapeTagNotFound matches a 404 that names a shape tag Vidispine does not have configured:
// a configuration problem, not a missing item.
var ErrShapeTagNotFound = errors.New("vidispine: shape tag not configured")

type Config struct {
	// BaseURL is the API root, for example "https://cantemo.example.com/API".
	BaseURL  string
	Username string
	Password string
	// HTTPClient defaults to a client with a 30 second timeout.
	HTTPClient *http.Client
	// Retries is how often a failed GET is repeated: 0 means the default of 3, negative means never.
	Retries int
	// RetryDelay is the pause before the first retry, doubled for each one after (default 200ms).
	RetryDelay time.Duration
}

type Client struct {
	base    *url.URL
	user    string
	pass    string
	http    *http.Client
	retries int
	delay   time.Duration
}

func New(cfg Config) (*Client, error) {
	base, err := url.Parse(strings.TrimRight(cfg.BaseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("vidispine: base URL: %w", err)
	}
	if base.Scheme == "" || base.Host == "" {
		return nil, errors.New("vidispine: base URL must be absolute")
	}
	c := &Client{base: base, user: cfg.Username, pass: cfg.Password, http: cfg.HTTPClient, retries: cfg.Retries, delay: cfg.RetryDelay}
	if c.http == nil {
		c.http = &http.Client{Timeout: defaultTimeout}
	}
	switch {
	case c.retries == 0:
		c.retries = defaultRetries
	case c.retries < 0:
		c.retries = 0
	}
	if c.delay <= 0 {
		c.delay = 200 * time.Millisecond
	}
	return c, nil
}

// Error is a response with a status other than 2xx.
type Error struct {
	Status int
	Method string
	URL    string
	// NotFoundType and NotFoundID name what Vidispine could not find, e.g. "item" and "VX-12".
	NotFoundType string
	NotFoundID   string
	// Body is the start of the response body.
	Body string
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("vidispine: %s %s: status %d", e.Method, e.URL, e.Status)
	if e.NotFoundType != "" {
		return fmt.Sprintf("%s: %s %q not found", msg, e.NotFoundType, e.NotFoundID)
	}
	if e.Body != "" {
		return msg + ": " + e.Body
	}
	return msg
}

func (e *Error) Is(target error) bool {
	switch target {
	case ErrNotFound:
		return e.Status == http.StatusNotFound
	case ErrShapeTagNotFound:
		return e.Status == http.StatusNotFound && e.NotFoundType == "shape-tag"
	}
	return false
}

func newError(method, u string, res *http.Response) *Error {
	e := &Error{Status: res.StatusCode, Method: method, URL: u}
	raw, _ := io.ReadAll(io.LimitReader(res.Body, errorBodyLimit))
	e.Body = strings.TrimSpace(string(raw))
	var envelope struct {
		NotFound *struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"notFound"`
	}
	if json.Unmarshal(raw, &envelope) == nil && envelope.NotFound != nil {
		e.NotFoundType, e.NotFoundID = envelope.NotFound.Type, envelope.NotFound.ID
	}
	return e
}

// endpoint builds a URL under the API root, escaping every path segment.
func (c *Client) endpoint(query url.Values, segments ...string) string {
	u := *c.base
	escaped := make([]string, len(segments))
	for i, s := range segments {
		escaped[i] = url.PathEscape(s)
	}
	u.Path = c.base.Path + "/" + strings.Join(segments, "/")
	u.RawPath = c.base.EscapedPath() + "/" + strings.Join(escaped, "/")
	u.RawQuery = query.Encode()
	return u.String()
}

// get decodes a JSON response into out. Only network errors and 5xx responses are retried:
// anything else would fail the same way again.
func (c *Client) get(ctx context.Context, out any, query url.Values, segments ...string) error {
	u := c.endpoint(query, segments...)
	delay := c.delay
	for attempt := 0; ; attempt++ {
		retry, err := c.getOnce(ctx, u, out)
		if err == nil || !retry || attempt >= c.retries {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(delay):
		}
		delay *= 2
	}
}

func (c *Client) getOnce(ctx context.Context, u string, out any) (retry bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, fmt.Errorf("vidispine: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if c.user != "" || c.pass != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return ctx.Err() == nil, fmt.Errorf("vidispine: GET %s: %w", u, err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return res.StatusCode >= 500, newError(http.MethodGet, u, res)
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, bodyLimit)).Decode(out); err != nil {
		return false, fmt.Errorf("vidispine: GET %s: decoding response: %w", u, err)
	}
	return false, nil
}
