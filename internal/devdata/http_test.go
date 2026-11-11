package devdata

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bcc-media/mediabrain/internal/domain"
	"github.com/bcc-media/mediabrain/internal/vidispineimport"
)

type fakeBackend struct {
	applyErr, resetErr, statsErr, usersErr error
	vidispineErr                           error
	gotImport                              vidispineimport.ImportInput
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

func (f *fakeBackend) VidispineConfigured() bool { return true }

func (f *fakeBackend) VidispineImport(_ context.Context, in vidispineimport.ImportInput) (VidispineAsset, error) {
	f.gotImport = in
	return VidispineAsset{ID: "AS-1", LegacyIDs: []string{in.ItemID}}, f.vidispineErr
}

func (f *fakeBackend) VidispineRefresh(_ context.Context, assetID string) (VidispineAsset, error) {
	return VidispineAsset{ID: assetID}, f.vidispineErr
}

// call makes a request the way the debug page does: a POST carries the handler's token.
func call(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	headers := map[string]string{}
	if method == http.MethodPost {
		headers[TokenHeader] = tokenOf(h)
	}
	return callWith(h, method, path, body, headers)
}

func callWith(h http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func tokenOf(h http.Handler) string {
	var got struct{ Token string }
	_ = json.Unmarshal(callWith(h, http.MethodGet, "/api/token", "", nil).Body.Bytes(), &got)
	return got.Token
}

// A POST is only accepted from the debug page or a local tool: with the token, and not from
// another origin. httptest requests have the host example.com.
func TestStateChangesNeedTokenAndSameOrigin(t *testing.T) {
	b := &fakeBackend{}
	h := Handler(b)
	token := tokenOf(h)
	if len(token) < 20 {
		t.Fatalf("token = %q", token)
	}
	if other := tokenOf(Handler(b)); other == token {
		t.Fatal("two handlers share a token")
	}

	paths := []string{"/api/reset", "/api/scenarios/catalog", "/api/users", "/api/vidispine/import", "/api/vidispine/refresh"}
	refused := map[string]map[string]string{
		"no token":             {},
		"wrong token":          {TokenHeader: token + "x"},
		"empty token":          {TokenHeader: ""},
		"other origin":         {TokenHeader: token, "Origin": "https://attacker.example"},
		"lookalike origin":     {TokenHeader: token, "Origin": "http://example.com.attacker.example"},
		"null origin":          {TokenHeader: token, "Origin": "null"},
		"cross-site fetch":     {TokenHeader: token, "Sec-Fetch-Site": "cross-site"},
		"same-site fetch":      {TokenHeader: token, "Sec-Fetch-Site": "same-site"},
		"cross-site, no token": {"Sec-Fetch-Site": "cross-site", "Origin": "https://attacker.example"},
	}
	for name, headers := range refused {
		for _, path := range paths {
			b.gotName = ""
			w := callWith(h, http.MethodPost, path, "{}", headers)
			if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"error"`) || b.gotName != "" {
				t.Errorf("%s: POST %s = %d %s", name, path, w.Code, w.Body)
			}
			if w.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Errorf("%s: POST %s sent a CORS header", name, path)
			}
		}
	}
	accepted := map[string]map[string]string{
		"curl":          {TokenHeader: token},
		"page":          {TokenHeader: token, "Origin": "http://example.com", "Sec-Fetch-Site": "same-origin"},
		"old browser":   {TokenHeader: token, "Origin": "http://example.com"},
		"typed address": {TokenHeader: token, "Sec-Fetch-Site": "none"},
	}
	for name, headers := range accepted {
		if w := callWith(h, http.MethodPost, "/api/reset", "", headers); w.Code != http.StatusOK {
			t.Errorf("%s: reset = %d %s", name, w.Code, w.Body)
		}
	}

	// Reading stays open (the page loads before it has a token), but the token itself is not
	// handed to a request another site started.
	for _, path := range []string{"/", "/api/stats", "/api/users", "/api/scenarios", "/api/vidispine"} {
		if w := callWith(h, http.MethodGet, path, "", map[string]string{"Sec-Fetch-Site": "cross-site"}); w.Code != http.StatusOK {
			t.Errorf("GET %s = %d", path, w.Code)
		}
	}
	for site, code := range map[string]int{"": 200, "same-origin": 200, "none": 200, "cross-site": 403, "same-site": 403} {
		w := callWith(h, http.MethodGet, "/api/token", "", map[string]string{"Sec-Fetch-Site": site})
		if w.Code != code || (code == 403) == strings.Contains(w.Body.String(), token) {
			t.Errorf("token with Sec-Fetch-Site %q = %d %s", site, w.Code, w.Body)
		}
		if w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("token response sent a CORS header")
		}
	}
}

// The page must send the header the handler checks, on every POST.
func TestPageSendsToken(t *testing.T) {
	page := call(Handler(&fakeBackend{}), http.MethodGet, "/", "").Body.String()
	for _, want := range []string{"fetch('api/token')", "headers['" + TokenHeader + "']"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
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

func TestVidispineEndpoints(t *testing.T) {
	b := &fakeBackend{}
	h := Handler(b)
	if w := call(h, http.MethodGet, "/api/vidispine", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"configured":true`) {
		t.Fatalf("status = %d %s", w.Code, w.Body)
	}
	w := call(h, http.MethodPost, "/api/vidispine/import", `{"itemId":"VX-1","typeId":2,"projectId":"PJ-1"}`)
	if w.Code != 200 || b.gotImport.ItemID != "VX-1" || b.gotImport.TypeID != 2 || b.gotImport.ProjectID == nil || *b.gotImport.ProjectID != "PJ-1" {
		t.Fatalf("import = %d %s, input %+v", w.Code, w.Body, b.gotImport)
	}
	if call(h, http.MethodPost, "/api/vidispine/import", `{"itemId":"VX-1","typeId":1,"projectId":""}`); b.gotImport.ProjectID != nil {
		t.Fatalf("an empty project must mean no project, got %q", *b.gotImport.ProjectID)
	}
	if w := call(h, http.MethodPost, "/api/vidispine/refresh", `{"assetId":"AS-9"}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"AS-9"`) {
		t.Fatalf("refresh = %d %s", w.Code, w.Body)
	}
	for _, path := range []string{"/api/vidispine/import", "/api/vidispine/refresh"} {
		if w := call(h, http.MethodPost, path, "{bad"); w.Code != 400 {
			t.Errorf("%s bad json = %d", path, w.Code)
		}
		for err, code := range map[error]int{
			domain.ErrInvalidInput: 400, domain.ErrNotFound: 404, domain.ErrConflict: 409,
			domain.ErrUnavailable: 503, errors.New("db"): 500,
		} {
			b.vidispineErr = err
			if w := call(h, http.MethodPost, path, "{}"); w.Code != code || !strings.Contains(w.Body.String(), "error") {
				t.Errorf("%s with %v = %d, want %d", path, err, w.Code, code)
			}
		}
		b.vidispineErr = nil
	}
}
