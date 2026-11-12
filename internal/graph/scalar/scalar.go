// Package scalar holds the custom GraphQL scalars.
package scalar

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/99designs/gqlgen/graphql"

	"github.com/bcc-media/mediabrain/internal/domain"
)

const dateLayout = "2006-01-02"

func MarshalDate(t time.Time) graphql.Marshaler {
	return graphql.WriterFunc(func(w io.Writer) { _, _ = fmt.Fprintf(w, "%q", t.Format(dateLayout)) })
}

func UnmarshalDate(v any) (time.Time, error) {
	s, _ := v.(string)
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return time.Time{}, domain.Invalid("Date must be a string like 2026-01-31")
	}
	return t, nil
}

func MarshalTime(t time.Time) graphql.Marshaler { return graphql.MarshalTime(t) }

// UnmarshalTime is gqlgen's own, with an error the API has a code for: a scalar's error is the
// one kind that reaches the client without passing through a service.
func UnmarshalTime(v any) (time.Time, error) {
	t, err := graphql.UnmarshalTime(v)
	if err != nil {
		return time.Time{}, domain.Invalid("Time must be a string like 2026-01-31T12:00:00Z")
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
