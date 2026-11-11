// Package vidispinetest is a fake Vidispine API for tests: an HTTP server holding items,
// storages and collections that tests fill in.
package vidispinetest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/bcc-media/mediabrain/pkg/vidispine"
)

// The credentials the server accepts.
const (
	Username = "tester"
	Password = "secret"
)

type item struct {
	fields map[string][]*vidispine.MetadataField
	shapes []vidispine.Shape
}

type Server struct {
	// URL is the API root to pass as vidispine.Config.BaseURL.
	URL string

	srv *httptest.Server

	mu          sync.Mutex
	items       map[string]*item
	storages    map[string][]vidispine.StorageMethod
	collections map[string][]string
	failStatus  int
	requests    int
}

func NewServer() *Server {
	s := &Server{items: map[string]*item{}, storages: map[string][]vidispine.StorageMethod{}, collections: map[string][]string{}}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	s.URL = s.srv.URL + "/API"
	return s
}

func (s *Server) Close() { s.srv.Close() }

// Config returns a client configuration for this server that does not retry.
func (s *Server) Config() vidispine.Config {
	return vidispine.Config{BaseURL: s.URL, Username: Username, Password: Password, Retries: -1}
}

// AddItem adds or replaces an item. fields are values that apply to the whole item.
func (s *Server) AddItem(id string, fields map[string]string, shapes ...vidispine.Shape) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it := &item{fields: map[string][]*vidispine.MetadataField{}, shapes: shapes}
	for name, value := range fields {
		it.fields[name] = []*vidispine.MetadataField{wholeItem(value)}
	}
	s.items[id] = it
}

// SetField replaces the values of one field of an existing item.
func (s *Server) SetField(id, name string, values ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fields := make([]*vidispine.MetadataField, len(values))
	for i, v := range values {
		fields[i] = wholeItem(v)
	}
	s.items[id].fields[name] = fields
}

// AddTimedValue adds a value that applies to a part of the item, as subclips do.
func (s *Server) AddTimedValue(id, name, value, start, end string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it := s.items[id]
	it.fields[name] = append(it.fields[name], &vidispine.MetadataField{Start: start, End: end, Value: value})
}

func (s *Server) RemoveItem(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, id)
}

// AddStorage adds a storage reachable through the given URIs.
func (s *Server) AddStorage(id string, uris ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	methods := make([]vidispine.StorageMethod, len(uris))
	for i, u := range uris {
		methods[i] = vidispine.StorageMethod{ID: id + "-M" + string(rune('1'+i)), URI: u, Read: true, Write: true, Browse: true}
	}
	s.storages[id] = methods
}

func (s *Server) AddCollection(id string, itemIDs ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.collections[id] = itemIDs
}

// Fail makes every request answer with status until Fail(0).
func (s *Server) Fail(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failStatus = status
}

// Requests returns how many requests the server has received.
func (s *Server) Requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

func wholeItem(value string) *vidispine.MetadataField {
	return &vidispine.MetadataField{Start: vidispine.MinusInf, End: vidispine.PlusInf, Value: value}
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests++

	if s.failStatus != 0 {
		writeJSON(w, s.failStatus, map[string]any{"internalServer": map[string]string{"message": "induced failure"}})
		return
	}
	if u, p, ok := r.BasicAuth(); !ok || u != Username || p != Password {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"notAuthorized": map[string]string{}})
		return
	}
	path, ok := strings.CutPrefix(r.URL.Path, "/API/")
	if !ok || r.Method != http.MethodGet {
		notFound(w, "resource", r.URL.Path)
		return
	}
	parts := strings.Split(path, "/")
	switch {
	case len(parts) == 2 && parts[0] == "item":
		s.serveItem(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "storage" && parts[2] == "method":
		methods, ok := s.storages[parts[1]]
		if !ok {
			notFound(w, "storage", parts[1])
			return
		}
		writeJSON(w, http.StatusOK, vidispine.StorageResult{Methods: methods})
	case len(parts) == 3 && parts[0] == "storage" && parts[2] == "file":
		s.serveFiles(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "collection" && parts[2] == "item":
		ids, ok := s.collections[parts[1]]
		if !ok {
			notFound(w, "collection", parts[1])
			return
		}
		out := vidispine.CollectionItemsResult{Hits: int64(len(ids))}
		for _, id := range ids {
			if it, ok := s.items[id]; ok {
				out.Items = append(out.Items, &vidispine.MetadataResult{ID: id, Terse: it.fields})
			}
		}
		writeJSON(w, http.StatusOK, out)
	default:
		notFound(w, "resource", r.URL.Path)
	}
}

func (s *Server) serveItem(w http.ResponseWriter, r *http.Request, id string) {
	it, ok := s.items[id]
	if !ok {
		notFound(w, "item", id)
		return
	}
	if r.URL.Query().Get("content") == "shape" {
		writeJSON(w, http.StatusOK, vidispine.ShapeResult{ID: id, Shape: it.shapes})
		return
	}
	fields := it.fields
	if want := r.URL.Query()["field"]; len(want) > 0 {
		fields = map[string][]*vidispine.MetadataField{}
		for _, name := range want {
			if v, ok := it.fields[name]; ok {
				fields[name] = v
			}
		}
	}
	writeJSON(w, http.StatusOK, vidispine.MetadataResult{ID: id, Terse: fields})
}

// serveFiles lists the files of all shapes on a storage, under the requested path.
func (s *Server) serveFiles(w http.ResponseWriter, r *http.Request, storageID string) {
	if _, ok := s.storages[storageID]; !ok {
		notFound(w, "storage", storageID)
		return
	}
	q := r.URL.Query()
	out := vidispine.FileSearchResult{}
	for _, it := range s.items {
		for i := range it.shapes {
			f := it.shapes[i].File()
			if f == nil || f.Storage != storageID || !strings.HasPrefix(f.Path, q.Get("path")) {
				continue
			}
			if state := q.Get("state"); state != "" && f.State != state {
				continue
			}
			out.Files = append(out.Files, *f)
		}
	}
	out.Hits = len(out.Files)
	writeJSON(w, http.StatusOK, out)
}

func notFound(w http.ResponseWriter, kind, id string) {
	writeJSON(w, http.StatusNotFound, map[string]any{"notFound": map[string]string{"type": kind, "id": id}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
