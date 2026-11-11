package vidispine

import (
	"context"
	"errors"
	"fmt"
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
