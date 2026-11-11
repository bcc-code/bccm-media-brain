package vidispineimport

import (
	"errors"
	"testing"

	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/domain"
	"github.com/bcc-media/mediabrain/pkg/vidispine"
)

func storage(uris ...string) *vidispine.StorageResult {
	s := &vidispine.StorageResult{}
	for _, u := range uris {
		s.Methods = append(s.Methods, vidispine.StorageMethod{URI: u})
	}
	return s
}

func TestLocationOf(t *testing.T) {
	cases := []struct {
		name string
		uris []string
		want string
		kind sqlc.LocationKind
	}{
		{"local path wins", []string{"s3://key:secret@bucket/", "file:///mnt/isilon/"}, "file:///mnt/isilon/", sqlc.LocationKindNas},
		{"s3 without its keys", []string{"s3://key:secret@bucket/media/"}, "s3://bucket/media/", sqlc.LocationKindS3},
		{"ftp without its password", []string{"ftp://user:pw@host/dir/"}, "ftp://host/dir/", sqlc.LocationKindNas},
		{"unusable addresses are skipped", []string{"", "no scheme", "://", "file:///srv/media"}, "file:///srv/media", sqlc.LocationKindNas},
	}
	for _, c := range cases {
		got, err := locationOf("VX-9", storage(c.uris...))
		if err != nil || got.BaseURI != c.want || got.Kind != c.kind || got.Name != "VX-9" || !got.Online {
			t.Errorf("%s: %+v, %v", c.name, got, err)
		}
	}
	for _, uris := range [][]string{nil, {"", "no scheme"}} {
		if _, err := locationOf("VX-9", storage(uris...)); !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("%v: err = %v", uris, err)
		}
	}
}

func TestItemID(t *testing.T) {
	cases := []struct {
		name     string
		metadata string
		legacy   []string
		want     string
	}{
		{"from the metadata", `{"vidispine":{"itemId":"VX-12"}}`, []string{"VX-99"}, "VX-12"},
		{"from the legacy ids", `{}`, []string{"LEG-1", "VX-34"}, "VX-34"},
		{"edited metadata is not trusted", `{"vidispine":{"itemId":"../storage"}}`, []string{"VX-56"}, "VX-56"},
		{"metadata of another shape", `{"vidispine":"VX-1"}`, nil, ""},
		{"broken metadata", `[`, nil, ""},
		{"not imported", `{"note":"x"}`, []string{"LEG-1"}, ""},
	}
	for _, c := range cases {
		if got := ItemID(sqlc.Asset{Metadata: []byte(c.metadata), LegacyIds: c.legacy}); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestNewSource(t *testing.T) {
	if src, err := NewSource("", "u", "p"); src != nil || err != nil {
		t.Fatalf("empty URL: %v, %v", src, err)
	}
	if src, err := NewSource("https://vidispine.example.com/API", "u", "p"); src == nil || err != nil {
		t.Fatalf("URL: %v, %v", src, err)
	}
	if _, err := NewSource("vidispine.example.com", "u", "p"); err == nil {
		t.Fatal("relative URL accepted")
	}
}
