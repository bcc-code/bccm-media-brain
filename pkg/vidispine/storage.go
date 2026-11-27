package vidispine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type StorageResult struct {
	Methods []StorageMethod `json:"method"`
}

// StorageMethod is one way of reaching a storage, such as a file:// path on the server.
type StorageMethod struct {
	ID             string `json:"id"`
	URI            string `json:"uri"`
	Read           bool   `json:"read"`
	Write          bool   `json:"write"`
	Browse         bool   `json:"browse"`
	LastSuccess    string `json:"lastSuccess"`
	LastFailure    string `json:"lastFailure"`
	FailureMessage string `json:"failureMessage"`
	Type           string `json:"type"`
}

// LocalPath returns the path of the storage's first file:// method, or "".
func (s *StorageResult) LocalPath() string {
	for _, m := range s.Methods {
		if p, ok := strings.CutPrefix(m.URI, "file://"); ok {
			return p
		}
	}
	return ""
}

func (c *Client) GetStorageMethods(ctx context.Context, storageID string) (*StorageResult, error) {
	out := &StorageResult{}
	return out, c.get(ctx, out, nil, "storage", storageID, "method")
}

// ErrTooLarge is returned by ReadFile for a file larger than the limit it was given.
var ErrTooLarge = errors.New("vidispine: file is larger than the limit")

// ReadFile returns the content of a file, such as a subtitle sidecar. A file larger than limit
// bytes is not read to its end: the error is ErrTooLarge.
func (c *Client) ReadFile(ctx context.Context, fileID string, limit int64) ([]byte, error) {
	u := c.endpoint(nil, "storage", "file", fileID, "data")
	var data []byte
	err := c.retrying(ctx, u, "*/*", func(body io.Reader) error {
		var err error
		if data, err = io.ReadAll(io.LimitReader(body, limit+1)); err != nil {
			return fmt.Errorf("vidispine: GET %s: %w", u, err)
		}
		if int64(len(data)) > limit {
			return fmt.Errorf("%w: file %s, limit %d bytes", ErrTooLarge, fileID, limit)
		}
		return nil
	})
	return data, err
}

type FileSearchResult struct {
	Hits  int    `json:"hits,omitempty"`
	Files []File `json:"file,omitempty"`
}

// Values of ListFilesParams.Filter.
const (
	FilterAllFiles          = "files"
	FilterAssociatedFiles   = "item"
	FilterUnassociatedFiles = "noitem"
)

type ListFilesParams struct {
	// Path is relative to the storage.
	Path      string
	Recursive bool
	First     int
	Number    int
	// State limits the listing to files in this state, e.g. FileStateClosed. Empty means any.
	State string
	// Filter narrows the listing by whether files belong to an item.
	Filter []string
}

func (c *Client) ListFilesForStorage(ctx context.Context, storageID string, p ListFilesParams) (*FileSearchResult, error) {
	q := url.Values{
		"path":        {p.Path},
		"recursive":   {strconv.FormatBool(p.Recursive)},
		"includeItem": {"true"},
		"first":       {strconv.Itoa(p.First)},
	}
	if p.Number > 0 {
		q.Set("number", strconv.Itoa(p.Number))
	}
	if p.State != "" {
		q.Set("state", p.State)
	}
	if len(p.Filter) > 0 {
		q.Set("filter", strings.Join(p.Filter, ","))
	}
	out := &FileSearchResult{}
	return out, c.get(ctx, out, q, "storage", storageID, "file")
}

// TCToSeconds converts a Vidispine time code ("<samples>@<time base>") to seconds. The time
// base is PAL (25 per second) or a number of samples per second, e.g. "180000@50" or
// "48000@48000".
func TCToSeconds(tc string) (float64, error) {
	samplesText, base, ok := strings.Cut(tc, "@")
	if !ok {
		return 0, errors.New("vidispine: invalid time code: " + tc)
	}
	samples, err := strconv.ParseFloat(samplesText, 64)
	if err != nil {
		return 0, fmt.Errorf("vidispine: invalid time code %s: %w", tc, err)
	}
	rate := 25.0
	if base != "PAL" {
		r, err := strconv.ParseInt(base, 10, 64)
		if err != nil || r <= 0 {
			return 0, errors.New("vidispine: unsupported time base: " + tc)
		}
		rate = float64(r)
	}
	return samples / rate, nil
}

// FileStream is the content of a file as Vidispine answers it: all of it (200), the part a
// range asked for (206), or a range that does not fit the file (416).
type FileStream struct {
	Status int
	// Header holds what a client needs to play or seek: Content-Type, Content-Length,
	// Content-Range, Accept-Ranges, Last-Modified and ETag, where Vidispine sent them.
	Header http.Header
	Body   io.ReadCloser
}

var streamHeaders = []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Last-Modified", "ETag"}

// OpenFile starts reading the content of a file, or only the bytes rangeHeader asks for (an
// HTTP Range value such as "bytes=0-1023"; empty for all). Unlike ReadFile it does not read
// the content: the caller reads and closes Body. It is not retried and has no timeout of its
// own, since a video may be read for as long as someone watches it; ctx ends it.
func (c *Client) OpenFile(ctx context.Context, fileID, rangeHeader string) (*FileStream, error) {
	u := c.endpoint(nil, "storage", "file", fileID, "data")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("vidispine: %w", err)
	}
	req.Header.Set("Accept", "*/*")
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	if c.user != "" || c.pass != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	res, err := c.stream.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vidispine: GET %s: %w", u, err)
	}
	if res.StatusCode != http.StatusRequestedRangeNotSatisfiable && (res.StatusCode < 200 || res.StatusCode > 299) {
		defer res.Body.Close()
		return nil, newError(http.MethodGet, u, res)
	}
	out := &FileStream{Status: res.StatusCode, Header: http.Header{}, Body: res.Body}
	for _, h := range streamHeaders {
		if v := res.Header.Get(h); v != "" {
			out.Header.Set(h, v)
		}
	}
	return out, nil
}
