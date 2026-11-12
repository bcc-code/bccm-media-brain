//go:build e2e

package e2e

import (
	"net/http"
	"testing"

	"github.com/bcc-media/mediabrain/internal/fake"
)

// Whatever is wrong with a request, the error says so in extensions.code: a client switches on
// the code and must never have to read the message.
func TestEveryErrorHasACode(t *testing.T) {
	admin, f := signInAdmin(t), fake.New(1211)
	prog := newProgram(t, admin, f)
	createProject := `mutation($in: CreateProjectInput!) { createProject(input: $in) { id } }`
	project := func(extra map[string]any) map[string]any {
		in := map[string]any{"programId": prog.ID, "code": prog.Code + "_ERR", "name": "Errors"}
		for k, v := range extra {
			in[k] = v
		}
		return map[string]any{"in": in}
	}
	cases := map[string]struct {
		query string
		vars  map[string]any
		code  string
	}{
		"a date that is none, as a literal": {
			`mutation { createProject(input: {programId: "` + prog.ID + `", code: "` + prog.Code + `_ERR", name: "x", startsOn: "31.01.2026"}) { id } }`, nil, "INVALID_INPUT"},
		"a date that is none, as a variable": {createProject, project(map[string]any{"startsOn": "31.01.2026"}), "INVALID_INPUT"},
		"a date of the wrong type":           {createProject, project(map[string]any{"startsOn": 20260131}), "INVALID_INPUT"},
		"a time that is none": {
			`mutation($t: Time) { grantAccess(input: {scope: PROGRAM, scopeId: "` + prog.ID + `", level: VIEW, userId: "` + admin.id + `", expiresAt: $t}) { id } }`,
			map[string]any{"t": "tomorrow"}, "INVALID_INPUT"},
		"a variable of the wrong type": {createProject, map[string]any{"in": "nothing"}, "GRAPHQL_VALIDATION_FAILED"},
		"a field that does not exist":  {`{ nothing }`, nil, "GRAPHQL_VALIDATION_FAILED"},
		"a query that does not parse":  {`{ ping `, nil, "GRAPHQL_PARSE_FAILED"},
		"a query that asks for too much": {
			`{ assets(limit: 500) { versions(limit: 100) { renditions { files { file { copies { id } } } } } } }`, nil, "COMPLEXITY_LIMIT_EXCEEDED"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			status, r := post(t, map[string]string{"Authorization": "Bearer " + admin.token}, c.query, c.vars)
			if status != http.StatusOK && status != http.StatusUnprocessableEntity {
				t.Fatalf("status %d: %+v", status, r.Errors)
			}
			if len(r.Errors) == 0 {
				t.Fatal("the request was accepted")
			}
			for _, e := range r.Errors {
				if e.Extensions.Code != c.code {
					t.Errorf("code %q, want %s: %s", e.Extensions.Code, c.code, e.Message)
				}
			}
		})
	}
}

// A conflict says which kind it is and an invalid input which field, in extensions: the
// message is for people.
func TestErrorsCarryWhatAClientActsOn(t *testing.T) {
	admin, f := signInAdmin(t), fake.New(1212)
	prog := newProgram(t, admin, f)
	projectID := newProject(t, admin, f, prog)

	e := admin.fails(t, "CONFLICT", `mutation($in: CreateProgramInput!) { createProgram(input: $in) { id } }`,
		map[string]any{"in": map[string]any{"code": prog.Code, "name": "Again"}})
	if e.Extensions.Reason != "ALREADY_EXISTS" || e.Extensions.CurrentRevision != nil {
		t.Errorf("a code that is taken: %+v", e.Extensions)
	}
	e = admin.fails(t, "CONFLICT", `mutation($id: ID!) { deleteProgram(id: $id) }`, map[string]any{"id": prog.ID})
	if e.Extensions.Reason != "IN_USE" {
		t.Errorf("a program that still has a project: %+v", e.Extensions)
	}
	e = admin.fails(t, "INVALID_INPUT", createAsset,
		map[string]any{"p": projectID, "in": map[string]any{"typeId": 32000, "title": "x"}})
	if e.Extensions.Field != "typeId" {
		t.Errorf("an asset type that does not exist: %+v", e.Extensions)
	}
	e = admin.fails(t, "INVALID_INPUT", `mutation($in: CreateProgramInput!) { createProgram(input: $in) { id } }`,
		map[string]any{"in": map[string]any{"code": "ZZZZ", "name": " "}})
	if e.Extensions.Field != "name" || e.Extensions.Reason != "" {
		t.Errorf("a blank name: %+v", e.Extensions)
	}
}
