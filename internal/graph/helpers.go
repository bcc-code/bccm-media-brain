package graph

import (
	"fmt"
	"strconv"

	"github.com/bcc-media/mediabrain/internal/dummy"
)

func parseID(id string) (int64, error) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%w: invalid id %q", dummy.ErrInvalidInput, id)
	}
	return n, nil
}

func deref(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
