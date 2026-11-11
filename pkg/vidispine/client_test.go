package vidispine_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bcc-media/mediabrain/pkg/vidispine"
	"github.com/bcc-media/mediabrain/pkg/vidispine/vidispinetest"
)

var ctx = context.Background()

func original(storage, path string) vidispine.Shape {
	return vidispine.Shape{
		ID: "VX-900", Tag: []string{vidispine.ShapeTagOriginal}, MimeType: []string{"video/quicktime"},
		ContainerComponent: vidispine.ContainerComponent{File: []vidispine.File{{
			ID: "VX-901", Path: path, Storage: storage, State: vidispine.FileStateClosed, Size: 1234,
			Hash: "0601f6d82d15cf20254370d062c839fc38163cfe",
			URI:  []string{"file:///mnt/isilon/" + strings.ReplaceAll(path, " ", "%20")},
		}}},
	}
}

func newServer(t *testing.T) (*vidispinetest.Server, *vidispine.Client) {
	t.Helper()
	srv := vidispinetest.NewServer()
	t.Cleanup(srv.Close)
	c, err := vidispine.New(srv.Config())
	if err != nil {
		t.Fatal(err)
	}
	return srv, c
}

// clientFor returns a client for a handler that stands in for the whole API.
func clientFor(t *testing.T, cfg vidispine.Config, h http.HandlerFunc) *vidispine.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	cfg.BaseURL = srv.URL + "/API/"
	c, err := vidispine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestMetadata(t *testing.T) {
	srv, c := newServer(t)
	srv.AddItem("VX-1", map[string]string{"title": "Sunday meeting", "empty": ""})
	srv.SetField("VX-1", "tags", "a", "b")
	srv.AddTimedValue("VX-1", "title", "Chapter 1", "100@PAL", "200@PAL")

	m, err := c.GetMetadata(ctx, "VX-1")
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "VX-1" || m.Get(vidispine.FieldTitle, "x") != "Sunday meeting" || m.Get("missing", "fallback") != "fallback" {
		t.Fatalf("metadata = %+v", m)
	}
	if got := m.GetArray("tags"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("tags = %v", got)
	}
	if got := m.GetArray("missing"); len(got) != 0 {
		t.Fatalf("missing = %v", got)
	}
	want := map[string][]string{"title": {"Sunday meeting"}, "tags": {"a", "b"}}
	if got := m.ItemValues(); !reflect.DeepEqual(got, want) {
		t.Fatalf("item values = %v, want %v", got, want)
	}

	only, err := c.GetMetadataFields(ctx, "VX-1", []string{"tags", "nope"})
	if err != nil || len(only.Terse) != 1 || len(only.Terse["tags"]) != 2 {
		t.Fatalf("fields = %+v, %v", only, err)
	}
}

func TestShapes(t *testing.T) {
	srv, c := newServer(t)
	sidecar := vidispine.Shape{ID: "VX-910", Tag: []string{"sub_nor_srt"}, BinaryComponent: []vidispine.BinaryComponent{
		{}, {File: []vidispine.File{{ID: "VX-911", URI: []string{"s3://bucket/x.srt"}}}},
	}}
	srv.AddItem("VX-1", nil, original("VX-42", "raw/2023/a b.mov"), sidecar, vidispine.Shape{ID: "VX-920", Tag: []string{"empty"}})

	shapes, err := c.GetShapes(ctx, "VX-1")
	if err != nil {
		t.Fatal(err)
	}
	o := shapes.GetShape(vidispine.ShapeTagOriginal)
	if o == nil || o.File().Hash == "" || o.GetPath() != "/mnt/isilon/raw/2023/a b.mov" {
		t.Fatalf("original = %+v", o)
	}
	if s := shapes.GetShape("sub_nor_srt"); s.File().ID != "VX-911" || s.GetPath() != "" {
		t.Fatalf("sidecar = %+v", s)
	}
	if s := shapes.GetShape("empty"); s.File() != nil || s.GetPath() != "" {
		t.Fatalf("empty shape has a file")
	}
	if shapes.GetShape(vidispine.ShapeTagLowres) != nil {
		t.Fatal("found a shape that is not there")
	}
	if p := (&vidispine.File{URI: []string{"file:///a%zz"}}).LocalPath(); p != "/a%zz" {
		t.Fatalf("badly escaped path = %q", p)
	}
}

func TestDecodesVidispineJSON(t *testing.T) {
	raw, err := os.ReadFile("testdata/shapes.json")
	if err != nil {
		t.Fatal(err)
	}
	c := clientFor(t, vidispine.Config{}, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/API/item/VX-1" || r.URL.Query().Get("content") != "shape" || r.Header.Get("Accept") != "application/json" {
			t.Errorf("unexpected request %s (Accept %q)", r.URL, r.Header.Get("Accept"))
		}
		if _, _, ok := r.BasicAuth(); ok {
			t.Error("credentials sent although none were configured")
		}
		_, _ = w.Write(raw)
	})
	shapes, err := c.GetShapes(ctx, "VX-1")
	if err != nil {
		t.Fatal(err)
	}
	o := shapes.GetShape(vidispine.ShapeTagOriginal)
	if o == nil {
		t.Fatal("no original shape")
	}
	f := o.File()
	if f.Storage != "VX-42" || f.Path != "raw/2023/08/01/EXAMPLE_20230801_PGM.mxf" || f.Size != 52428800 || len(f.Hash) != 40 {
		t.Fatalf("file = %+v", f)
	}
	v := o.VideoComponent[0]
	if v.Codec != "mpeg2video" || v.Resolution != (vidispine.Resolution{Width: 1920, Height: 1080}) {
		t.Fatalf("video = %+v", v)
	}
	if secs, ok := o.ContainerComponent.Duration.Seconds(); !ok || secs != 3600 {
		t.Fatalf("duration = %v, %v", secs, ok)
	}
	if len(o.AudioComponent) != 2 || o.AudioComponent[0].ChannelCount != 1 {
		t.Fatalf("audio = %+v", o.AudioComponent)
	}
}

func TestStoragesFilesAndCollections(t *testing.T) {
	srv, c := newServer(t)
	srv.AddStorage("VX-42", "s3://bucket/", "file:///mnt/isilon/")
	srv.AddStorage("VX-43", "s3://other/")
	open := original("VX-42", "raw/open.mov")
	open.ContainerComponent.File[0].State = "OPEN"
	srv.AddItem("VX-1", map[string]string{"title": "one"}, original("VX-42", "raw/a.mov"), open)
	srv.AddItem("VX-2", map[string]string{"title": "two"}, original("VX-43", "raw/b.mov"), vidispine.Shape{})
	srv.AddCollection("VX-7", "VX-1", "VX-2", "VX-gone")

	st, err := c.GetStorageMethods(ctx, "VX-42")
	if err != nil || len(st.Methods) != 2 || st.LocalPath() != "/mnt/isilon/" {
		t.Fatalf("storage = %+v, %v", st, err)
	}
	if st, _ := c.GetStorageMethods(ctx, "VX-43"); st.LocalPath() != "" {
		t.Fatalf("local path of an s3 storage = %q", st.LocalPath())
	}

	files, err := c.ListFilesForStorage(ctx, "VX-42", vidispine.ListFilesParams{
		Path: "raw/", Recursive: true, Number: 10, State: vidispine.FileStateClosed, Filter: []string{vidispine.FilterAssociatedFiles},
	})
	if err != nil || files.Hits != 1 || files.Files[0].Path != "raw/a.mov" {
		t.Fatalf("files = %+v, %v", files, err)
	}
	if all, err := c.ListFilesForStorage(ctx, "VX-42", vidispine.ListFilesParams{}); err != nil || all.Hits != 2 {
		t.Fatalf("all files = %+v, %v", all, err)
	}

	col, err := c.GetItemsInCollection(ctx, "VX-7", 100)
	if err != nil || col.Hits != 3 || len(col.Items) != 2 || col.Items[1].Get("title", "") != "two" {
		t.Fatalf("collection = %+v, %v", col, err)
	}
	for _, n := range []int{0, vidispine.MaxCollectionItems + 1} {
		if _, err := c.GetItemsInCollection(ctx, "VX-7", n); err == nil {
			t.Errorf("number %d accepted", n)
		}
	}
}

func TestNotFound(t *testing.T) {
	srv, c := newServer(t)
	calls := map[string]func() error{
		"item":       func() error { _, err := c.GetMetadata(ctx, "VX-404"); return err },
		"shapes":     func() error { _, err := c.GetShapes(ctx, "VX-404"); return err },
		"storage":    func() error { _, err := c.GetStorageMethods(ctx, "VX-404"); return err },
		"files":      func() error { _, err := c.ListFilesForStorage(ctx, "VX-404", vidispine.ListFilesParams{}); return err },
		"collection": func() error { _, err := c.GetItemsInCollection(ctx, "VX-404", 1); return err },
		"escaped id": func() error { _, err := c.GetMetadata(ctx, "a/b?c"); return err },
	}
	for name, call := range calls {
		before := srv.Requests()
		err := call()
		var ve *vidispine.Error
		if !errors.Is(err, vidispine.ErrNotFound) || errors.Is(err, vidispine.ErrShapeTagNotFound) || !errors.As(err, &ve) {
			t.Errorf("%s: err = %v", name, err)
			continue
		}
		if ve.Status != 404 || ve.NotFoundID == "" || !strings.Contains(err.Error(), "not found") {
			t.Errorf("%s: error = %+v", name, ve)
		}
		if srv.Requests() != before+1 {
			t.Errorf("%s: a 404 was retried", name)
		}
	}
	// The id is one path segment however it is spelled.
	var ve *vidispine.Error
	if _, err := c.GetMetadata(ctx, "a/b?c"); !errors.As(err, &ve) || !strings.Contains(ve.URL, "/API/item/a%2Fb%3Fc?") {
		t.Fatalf("escaped id: %v", err)
	}
}

func TestErrors(t *testing.T) {
	srv, c := newServer(t)
	srv.AddItem("VX-1", nil)

	srv.Fail(http.StatusInternalServerError)
	_, err := c.GetMetadata(ctx, "VX-1")
	var ve *vidispine.Error
	if !errors.As(err, &ve) || ve.Status != 500 || errors.Is(err, vidispine.ErrNotFound) || !strings.Contains(err.Error(), "induced failure") {
		t.Fatalf("500: %v", err)
	}
	if errors.Is(err, errors.New("other")) {
		t.Fatal("matched an unrelated error")
	}
	srv.Fail(0)

	wrong := srv.Config()
	wrong.Password = "nope"
	bad, _ := vidispine.New(wrong)
	if _, err := bad.GetMetadata(ctx, "VX-1"); !errors.As(err, &ve) || ve.Status != http.StatusUnauthorized {
		t.Fatalf("wrong password: %v", err)
	}

	shapeTag := clientFor(t, vidispine.Config{Retries: -1}, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"notFound":{"type":"shape-tag","id":"lowres"}}`))
	})
	if _, err := shapeTag.GetShapes(ctx, "VX-1"); !errors.Is(err, vidispine.ErrShapeTagNotFound) || !errors.Is(err, vidispine.ErrNotFound) {
		t.Fatalf("shape tag: %v", err)
	}

	empty := clientFor(t, vidispine.Config{Retries: -1}, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	if _, err := empty.GetShapes(ctx, "VX-1"); err == nil || !strings.HasSuffix(err.Error(), "status 403") {
		t.Fatalf("empty body: %v", err)
	}

	garbage := clientFor(t, vidispine.Config{}, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{")) })
	if _, err := garbage.GetShapes(ctx, "VX-1"); err == nil || !strings.Contains(err.Error(), "decoding") {
		t.Fatalf("garbage: %v", err)
	}

	for _, base := range []string{"://", "/relative", ""} {
		if _, err := vidispine.New(vidispine.Config{BaseURL: base}); err == nil {
			t.Errorf("base URL %q accepted", base)
		}
	}
}

func TestRetries(t *testing.T) {
	var calls atomic.Int32
	flaky := func(failures int32) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			if calls.Add(1) <= failures {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			_, _ = w.Write([]byte(`{"id":"VX-1"}`))
		}
	}
	fast := vidispine.Config{RetryDelay: time.Millisecond}

	c := clientFor(t, fast, flaky(2))
	if m, err := c.GetMetadata(ctx, "VX-1"); err != nil || m.ID != "VX-1" || calls.Load() != 3 {
		t.Fatalf("recovering: %v after %d calls", err, calls.Load())
	}

	calls.Store(0)
	c = clientFor(t, fast, flaky(100))
	if _, err := c.GetMetadata(ctx, "VX-1"); err == nil || calls.Load() != 4 {
		t.Fatalf("giving up: %v after %d calls", err, calls.Load())
	}

	calls.Store(0)
	c = clientFor(t, vidispine.Config{Retries: -1}, flaky(100))
	if _, err := c.GetMetadata(ctx, "VX-1"); err == nil || calls.Load() != 1 {
		t.Fatalf("no retries: %v after %d calls", err, calls.Load())
	}

	// A cancelled context ends the wait between attempts.
	calls.Store(0)
	c = clientFor(t, vidispine.Config{RetryDelay: time.Hour}, flaky(100))
	cancelled, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, err := c.GetMetadata(cancelled, "VX-1"); err == nil || calls.Load() != 1 {
		t.Fatalf("cancelled: %v after %d calls", err, calls.Load())
	}

	done, cancelNow := context.WithCancel(ctx)
	cancelNow()
	if _, err := c.GetMetadata(done, "VX-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("already cancelled: %v", err)
	}

	unreachable, _ := vidispine.New(vidispine.Config{BaseURL: "http://127.0.0.1:1/API", Retries: 1, RetryDelay: time.Millisecond})
	if _, err := unreachable.GetMetadata(ctx, "VX-1"); err == nil {
		t.Fatal("unreachable server gave no error")
	}
}

func TestTCToSeconds(t *testing.T) {
	cases := []struct {
		in      string
		want    float64
		wantErr bool
	}{
		{"0@PAL", 0, false},
		{"25@PAL", 1, false},
		{"90000@PAL", 3600, false},
		{"180000@50", 3600, false},
		{"48000@48000", 1, false},
		{"25000@NTSC", 0, true},
		{"25000@0", 0, true},
		{"x@PAL", 0, true},
		{"25000", 0, true},
	}
	for _, c := range cases {
		got, err := vidispine.TCToSeconds(c.in)
		if got != c.want || (err != nil) != c.wantErr {
			t.Errorf("TCToSeconds(%q) = %v, %v", c.in, got, err)
		}
	}
}

func TestTimestampAndIntervals(t *testing.T) {
	if _, ok := (vidispine.Timestamp{Samples: 10}).Seconds(); ok {
		t.Error("a timestamp without a time base has seconds")
	}
	if s, ok := (vidispine.Timestamp{Samples: 50, TimeBase: vidispine.Fraction{Numerator: 1, Denominator: 25}}).Seconds(); !ok || s != 2 {
		t.Errorf("seconds = %v, %v", s, ok)
	}
	for f, want := range map[vidispine.MetadataField]bool{
		{}: true,
		{Start: vidispine.MinusInf, End: vidispine.PlusInf}: true,
		{Start: "1@PAL", End: vidispine.PlusInf}:            false,
		{Start: vidispine.MinusInf, End: "9@PAL"}:           false,
	} {
		if f.WholeItem() != want {
			t.Errorf("WholeItem(%+v) = %v", f, !want)
		}
	}
	m := &vidispine.MetadataResult{Terse: map[string][]*vidispine.MetadataField{"title": {nil}}}
	if m.Get("title", "none") != "none" || len(m.GetArray("title")) != 0 || len(m.ItemValues()) != 0 {
		t.Error("nil values are not skipped")
	}
}
