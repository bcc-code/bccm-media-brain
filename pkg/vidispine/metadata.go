package vidispine

import (
	"context"
	"fmt"
	"net/url"
)

// Field names that every Vidispine installation has. Portal's own fields ("portal_mf…") differ
// per installation and are passed as plain strings.
const (
	FieldTitle            = "title"
	FieldOriginalFilename = "originalFilename"
	FieldDurationSeconds  = "durationSeconds"
	FieldStartTimeCode    = "startTimeCode"
	FieldSequenceSize     = "__sequence_size"
)

// MinusInf and PlusInf are the interval of a value that applies to the whole item.
const (
	MinusInf = "-INF"
	PlusInf  = "+INF"
)

// MetadataField is one value of a field. Start and End are time codes ("<samples>@<time base>")
// when the value belongs to a part of the item, such as a subclip.
type MetadataField struct {
	End   string `json:"end"`
	Start string `json:"start"`
	UUID  string `json:"uuid"`
	Value string `json:"value"`
}

// WholeItem reports whether the value applies to the whole item and not to a part of it.
func (f MetadataField) WholeItem() bool {
	return (f.Start == "" || f.Start == MinusInf) && (f.End == "" || f.End == PlusInf)
}

// MetadataResult is an item's metadata in Vidispine's terse form: values per field name.
type MetadataResult struct {
	Terse map[string][]*MetadataField `json:"terse"`
	ID    string                      `json:"id"`
}

// Get returns the first value of the field, or fallback when the field is absent. It does not
// look at which part of the item the value belongs to.
func (m *MetadataResult) Get(field, fallback string) string {
	for _, v := range m.Terse[field] {
		if v != nil {
			return v.Value
		}
	}
	return fallback
}

func (m *MetadataResult) GetArray(field string) []string {
	out := []string{}
	for _, v := range m.Terse[field] {
		if v != nil {
			out = append(out, v.Value)
		}
	}
	return out
}

// ItemValues returns the non-empty values that apply to the whole item, per field. Values of
// subclips and other intervals are left out.
func (m *MetadataResult) ItemValues() map[string][]string {
	out := map[string][]string{}
	for name, values := range m.Terse {
		for _, v := range values {
			if v != nil && v.Value != "" && v.WholeItem() {
				out[name] = append(out[name], v.Value)
			}
		}
	}
	return out
}

func (c *Client) GetMetadata(ctx context.Context, itemID string) (*MetadataResult, error) {
	return c.GetMetadataFields(ctx, itemID, nil)
}

// GetMetadataFields returns only the named fields; no names means all of them.
func (c *Client) GetMetadataFields(ctx context.Context, itemID string, fields []string) (*MetadataResult, error) {
	q := url.Values{"content": {"metadata"}, "terse": {"true"}}
	for _, f := range fields {
		q.Add("field", f)
	}
	out := &MetadataResult{}
	return out, c.get(ctx, out, q, "item", itemID)
}

type CollectionItemsResult struct {
	Hits  int64             `json:"hits"`
	Items []*MetadataResult `json:"item"`
}

// MaxCollectionItems is the most items Vidispine returns for one collection request.
const MaxCollectionItems = 1000

// GetItemsInCollection returns up to number items of a collection with their metadata.
func (c *Client) GetItemsInCollection(ctx context.Context, collectionID string, number int) (*CollectionItemsResult, error) {
	if number < 1 || number > MaxCollectionItems {
		return nil, fmt.Errorf("vidispine: number must be 1..%d, got %d", MaxCollectionItems, number)
	}
	q := url.Values{"content": {"metadata"}, "children": {"item"}, "terse": {"true"}, "number": {fmt.Sprint(number)}}
	out := &CollectionItemsResult{}
	return out, c.get(ctx, out, q, "collection", collectionID, "item")
}
