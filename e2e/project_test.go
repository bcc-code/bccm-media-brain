//go:build e2e

package e2e

import (
	"testing"

	"github.com/bcc-media/mediabrain/internal/fake"
)

const (
	createProgram = `mutation($in: CreateProgramInput!) { createProgram(input: $in) { id code name description metadata createdAt updatedAt } }`
	createProject = `mutation($in: CreateProjectInput!) { createProject(input: $in) { id code status startsOn endsOn program { id code } } }`
	updateProject = `mutation($id: ID!, $in: UpdateProjectInput!) { updateProject(id: $id, input: $in) { id name status startsOn endsOn metadata } }`
)

func TestProgramsAndProjects(t *testing.T) {
	admin, f := signInAdmin(t), fake.New(1)

	in := f.Program()
	var created struct {
		CreateProgram struct {
			ID, Code, Name, Description string
			Metadata                    map[string]any
		}
	}
	vars := map[string]any{"in": map[string]any{"code": in.Code, "name": "  " + in.Name + " ", "description": in.Description, "metadata": in.Metadata}}
	admin.must(t, createProgram, vars, &created)
	prog := created.CreateProgram
	if prog.Code != in.Code || prog.Name != in.Name || prog.Description != *in.Description || prog.Metadata["season"] == nil {
		t.Fatalf("created = %+v", prog)
	}
	admin.fails(t, "CONFLICT", createProgram, vars) // the code is unique

	var updated struct {
		UpdateProgram struct {
			Name        string
			Description *string
			Metadata    map[string]any
		}
	}
	admin.must(t, `mutation($id: ID!) { updateProgram(id: $id, input: {name: "Renamed"}) { name description metadata } }`,
		map[string]any{"id": prog.ID}, &updated)
	if updated.UpdateProgram.Name != "Renamed" || updated.UpdateProgram.Description != nil || len(updated.UpdateProgram.Metadata) != 0 {
		t.Fatalf("update replaces all editable fields, got %+v", updated.UpdateProgram)
	}

	p := program{ID: prog.ID, Code: prog.Code}
	pv := projectVars(f, p)
	pv["status"] = "ACTIVE"
	var pj struct {
		CreateProject struct {
			ID, Code, Status, StartsOn, EndsOn string
			Program                            program
		}
	}
	admin.must(t, createProject, map[string]any{"in": pv}, &pj)
	if got := pj.CreateProject; got.Status != "ACTIVE" || got.StartsOn != pv["startsOn"] || got.EndsOn != pv["endsOn"] || got.Program != p {
		t.Fatalf("project = %+v", got)
	}
	second := newProject(t, admin, f, p)

	var up struct {
		UpdateProject struct {
			Name, Status string
			StartsOn     *string
		}
	}
	admin.must(t, updateProject, map[string]any{"id": second, "in": map[string]any{"name": "Second", "status": "DELIVERED"}}, &up)
	if up.UpdateProject.Name != "Second" || up.UpdateProject.Status != "DELIVERED" || up.UpdateProject.StartsOn != nil {
		t.Fatalf("updated project = %+v", up.UpdateProject)
	}

	var list struct {
		Program  struct{ Projects []struct{ ID string } }
		Projects []struct{ ID string }
		Programs []struct{ ID string }
	}
	admin.must(t, `query($p: ID!) {
		program(id: $p) { projects { id } }
		projects(programId: $p, status: DELIVERED) { id }
		programs(limit: 500) { id }
	}`, map[string]any{"p": prog.ID}, &list)
	if len(list.Program.Projects) != 2 || len(list.Projects) != 1 || list.Projects[0].ID != second || len(list.Programs) == 0 {
		t.Fatalf("lists = %+v", list)
	}

	const del = `mutation($id: ID!) { deleteProgram(id: $id) }`
	admin.fails(t, "CONFLICT", del, map[string]any{"id": prog.ID}) // still has projects
	for _, id := range []string{pj.CreateProject.ID, second} {
		admin.must(t, `mutation($id: ID!) { deleteProject(id: $id) }`, map[string]any{"id": id}, nil)
		admin.isNull(t, "project", id)
	}
	admin.must(t, del, map[string]any{"id": prog.ID}, nil)
	admin.isNull(t, "program", prog.ID)
	admin.fails(t, "NOT_FOUND", del, map[string]any{"id": prog.ID})
	admin.fails(t, "NOT_FOUND", `mutation($id: ID!) { deleteProject(id: $id) }`, map[string]any{"id": second})
}

func TestProgramAndProjectValidation(t *testing.T) {
	admin, f := signInAdmin(t), fake.New(2)
	p := newProgram(t, admin, f)
	project := newProject(t, admin, f, p)

	program := func(patch map[string]any) map[string]any {
		in := map[string]any{"code": "ABCDE", "name": "Name"}
		for k, v := range patch {
			in[k] = v
		}
		return map[string]any{"in": in}
	}
	for name, vars := range map[string]map[string]any{
		"lowercase code": program(map[string]any{"code": "abcd"}),
		"short code":     program(map[string]any{"code": "ABC"}),
		"long code":      program(map[string]any{"code": "ABCDEF"}),
		"blank name":     program(map[string]any{"name": "  "}),
		"metadata array": program(map[string]any{"metadata": []int{1}}),
	} {
		t.Run("program/"+name, func(t *testing.T) { admin.fails(t, "INVALID_INPUT", createProgram, vars) })
	}

	withProject := func(patch map[string]any) map[string]any {
		in := projectVars(f, p)
		for k, v := range patch {
			in[k] = v
		}
		return map[string]any{"in": in}
	}
	for name, vars := range map[string]map[string]any{
		"foreign prefix": withProject(map[string]any{"code": "ZZZZ_S01"}),
		"no suffix":      withProject(map[string]any{"code": p.Code}),
		"bad characters": withProject(map[string]any{"code": p.Code + "_a-b"}),
		"blank name":     withProject(map[string]any{"name": ""}),
		"ends first":     withProject(map[string]any{"startsOn": "2026-02-01", "endsOn": "2026-01-01"}),
		"metadata text":  withProject(map[string]any{"metadata": "x"}),
	} {
		t.Run("project/"+name, func(t *testing.T) { admin.fails(t, "INVALID_INPUT", createProject, vars) })
	}
	admin.fails(t, "NOT_FOUND", createProject, withProject(map[string]any{"programId": "PG-00000000000000000000000000"}))
	admin.fails(t, "INVALID_INPUT", updateProject, map[string]any{"id": project, "in": map[string]any{"name": " "}})
	admin.fails(t, "INVALID_INPUT", updateProject, map[string]any{"id": project, "in": map[string]any{"name": "x", "startsOn": "2026-02-01", "endsOn": "2026-01-01"}})
	admin.fails(t, "INVALID_INPUT", updateProject, map[string]any{"id": project, "in": map[string]any{"name": "x", "metadata": 5}})
	admin.fails(t, "NOT_FOUND", updateProject, map[string]any{"id": "PJ-00000000000000000000000000", "in": map[string]any{"name": "x"}})
	admin.fails(t, "INVALID_INPUT", `mutation($id: ID!) { updateProgram(id: $id, input: {name: ""}) { id } }`, map[string]any{"id": p.ID})
	admin.fails(t, "INVALID_INPUT", `{ programs(limit: -1) { id } }`, nil)
	admin.fails(t, "INVALID_INPUT", `{ projects(offset: -1) { id } }`, nil)

	// A date that is not a date is refused before it reaches the service.
	if errs := admin.gql(t, createProject, withProject(map[string]any{"startsOn": "01.02.2026"}), nil); len(errs) == 0 || !contains(errs[0].Message, "Date") {
		t.Fatalf("bad date: %+v", errs)
	}
}
