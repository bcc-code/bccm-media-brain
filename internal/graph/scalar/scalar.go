// Package scalar holds the custom GraphQL scalars.
package scalar

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/99designs/gqlgen/graphql"
)

const dateLayout = "2006-01-02"

func MarshalDate(t time.Time) graphql.Marshaler {
	return graphql.WriterFunc(func(w io.Writer) { _, _ = fmt.Fprintf(w, "%q", t.Format(dateLayout)) })
}

func UnmarshalDate(v any) (time.Time, error) {
	s, ok := v.(string)
	if !ok {
		return time.Time{}, fmt.Errorf("Date must be a string like 2026-01-31")
	}
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("Date must be a string like 2026-01-31")
	}
	return t, nil
}

func MarshalJSON(raw json.RawMessage) graphql.Marshaler {
	return graphql.WriterFunc(func(w io.Writer) {
		if len(raw) == 0 {
			raw = json.RawMessage("null")
		}
		_, _ = w.Write(raw)
	})
}

func UnmarshalJSON(v any) (json.RawMessage, error) {
	return json.Marshal(v)
}
