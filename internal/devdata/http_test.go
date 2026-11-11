package devdata

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bcc-media/mediabrain/internal/cantemo"
	"github.com/bcc-media/mediabrain/internal/domain"
)

type fakeBackend struct {
	applyErr, resetErr, statsErr, usersErr error
	cantemoErr                             error
	gotImport                              cantemo.ImportInput
	gotName                                string
	gotReq                                 ApplyRequest
}

func (f *fakeBackend) Apply(_ context.Context, name string, req ApplyRequest) (Result, error) {
	f.gotName, f.gotReq = name, req
	return Result{Scenario: name, Summary: "done", Seed: 7}, f.applyErr
}
func (f *fakeBackend) Reset(context.Context) error { return f.resetErr }
func (f *fakeBackend) Stats(context.Context) ([]TableStat, error) {
	return []TableStat{{Table: "asset", Rows: 3}}, f.statsErr
}

func (f *fakeBackend) Users(context.Context) ([]User, error) {
	return []User{{ID: "US-1", Admin: true}}, f.usersErr
}

func (f *fakeBackend) CreateUser(_ context.Context, email string, admin bool) (User, error) {
	return User{ID: "US-2", Email: &email, Admin: admin}, f.usersErr
}

func (f *fakeBackend) CantemoConfigured() bool { return true }

func (f *fakeBackend) CantemoImport(_ context.Context, in cantemo.ImportInput) (CantemoAsset, error) {
	f.gotImport = in
	return CantemoAsset{ID: "AS-1", LegacyIDs: []string{in.ItemID}}, f.cantemoErr
}

func (f *fakeBackend) CantemoRefresh(_ context.Context, assetID string) (CantemoAsset, error) {
	return CantemoAsset{ID: assetID}, f.cantemoErr
}

func call(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestUI(t *testing.T) {
	w := call(Handler(&fakeBackend{}), http.MethodGet, "/", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "MediaBrain debug") {
		t.Fatalf("ui = %d", w.Code)
	}
}

func TestScenariosEndpoint(t *testing.T) {
	w := call(Handler(&fakeBackend{}), http.MethodGet, "/api/scenarios", "")
	var got []Scenario
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || len(got) != len(Scenarios()) {
		t.Fatalf("scenarios = %s, %v", w.Body, err)
	}
}

func TestApplyEndpoint(t *testing.T) {
	b := &fakeBackend{}
	h := Handler(b)

	w := call(h, http.MethodPost, "/api/scenarios/catalog", `{"reset":true,"seed":3,"params":{"count":"2"}}`)
	if w.Code != 200 || b.gotName != "catalog" || !b.gotReq.Reset || b.gotReq.Seed != 3 || b.gotReq.Params["count"] != "2" {
		t.Fatalf("apply = %d %s, req %+v", w.Code, w.Body, b.gotReq)
	}
	if w := call(h, http.MethodPost, "/api/scenarios/empty", ""); w.Code != 200 {
		t.Fatalf("empty body = %d", w.Code)
	}
	if w := call(h, http.MethodPost, "/api/scenarios/x", "{bad"); w.Code != 400 {
		t.Fatalf("bad json = %d", w.Code)
	}
	for err, code := range map[error]int{
		ErrUnknownScenario:   404,
		ErrInvalidParams:     400,
		errors.New("db err"): 500,
	} {
		b.applyErr = err
		if w := call(h, http.MethodPost, "/api/scenarios/x", "{}"); w.Code != code || !strings.Contains(w.Body.String(), "error") {
			t.Errorf("%v → %d, want %d", err, w.Code, code)
		}
	}
}

func TestStatsAndReset(t *testing.T) {
	b := &fakeBackend{}
	h := Handler(b)
	if w := call(h, http.MethodGet, "/api/stats", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"rows":3`) {
		t.Fatalf("stats = %d %s", w.Code, w.Body)
	}
	if w := call(h, http.MethodPost, "/api/reset", ""); w.Code != 200 {
		t.Fatalf("reset = %d", w.Code)
	}
	b.statsErr, b.resetErr = errors.New("x"), errors.New("y")
	if w := call(h, http.MethodGet, "/api/stats", ""); w.Code != 500 {
		t.Fatalf("stats err = %d", w.Code)
	}
	if w := call(h, http.MethodPost, "/api/reset", ""); w.Code != 500 {
		t.Fatalf("reset err = %d", w.Code)
	}
}

func TestUsers(t *testing.T) {
	b := &fakeBackend{}
	h := Handler(b)
	if w := call(h, http.MethodGet, "/api/users", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"US-1"`) {
		t.Fatalf("list = %d %s", w.Code, w.Body)
	}
	if w := call(h, http.MethodPost, "/api/users", `{"email":"a@example.test","admin":true}`); w.Code != 200 ||
		!strings.Contains(w.Body.String(), `"a@example.test"`) || !strings.Contains(w.Body.String(), `"admin":true`) {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	if w := call(h, http.MethodPost, "/api/users", "{bad"); w.Code != 400 {
		t.Fatalf("bad json = %d", w.Code)
	}
	for err, code := range map[error]int{domain.ErrConflict: 400, domain.ErrInvalidInput: 400, errors.New("db"): 500} {
		b.usersErr = err
		if w := call(h, http.MethodPost, "/api/users", `{"email":"a"}`); w.Code != code {
			t.Errorf("create with %v = %d", err, w.Code)
		}
	}
	if w := call(h, http.MethodGet, "/api/users", ""); w.Code != 500 {
		t.Fatalf("list error = %d", w.Code)
	}
}

func TestCantemoEndpoints(t *testing.T) {
	b := &fakeBackend{}
	h := Handler(b)
	if w := call(h, http.MethodGet, "/api/cantemo", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"configured":true`) {
		t.Fatalf("status = %d %s", w.Code, w.Body)
	}
	w := call(h, http.MethodPost, "/api/cantemo/import", `{"itemId":"VX-1","typeId":2,"projectId":"PJ-1"}`)
	if w.Code != 200 || b.gotImport.ItemID != "VX-1" || b.gotImport.TypeID != 2 || b.gotImport.ProjectID == nil || *b.gotImport.ProjectID != "PJ-1" {
		t.Fatalf("import = %d %s, input %+v", w.Code, w.Body, b.gotImport)
	}
	if call(h, http.MethodPost, "/api/cantemo/import", `{"itemId":"VX-1","typeId":1,"projectId":""}`); b.gotImport.ProjectID != nil {
		t.Fatalf("an empty project must mean no project, got %q", *b.gotImport.ProjectID)
	}
	if w := call(h, http.MethodPost, "/api/cantemo/refresh", `{"assetId":"AS-9"}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"AS-9"`) {
		t.Fatalf("refresh = %d %s", w.Code, w.Body)
	}
	for _, path := range []string{"/api/cantemo/import", "/api/cantemo/refresh"} {
		if w := call(h, http.MethodPost, path, "{bad"); w.Code != 400 {
			t.Errorf("%s bad json = %d", path, w.Code)
		}
		for err, code := range map[error]int{
			domain.ErrInvalidInput: 400, domain.ErrNotFound: 404, domain.ErrConflict: 409,
			domain.ErrUnavailable: 503, errors.New("db"): 500,
		} {
			b.cantemoErr = err
			if w := call(h, http.MethodPost, path, "{}"); w.Code != code || !strings.Contains(w.Body.String(), "error") {
				t.Errorf("%s with %v = %d, want %d", path, err, w.Code, code)
			}
		}
		b.cantemoErr = nil
	}
}
