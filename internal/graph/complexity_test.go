package graph

import (
	"context"
	"strings"
	"testing"

	gqlcomplexity "github.com/99designs/gqlgen/complexity"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/bcc-media/mediabrain/internal/graph/generated"
)

func cost(t *testing.T, query string, vars map[string]any) int {
	t.Helper()
	es := generated.NewExecutableSchema(generated.Config{Resolvers: &Resolver{}, Complexity: complexity()})
	doc, errs := gqlparser.LoadQuery(es.Schema(), query)
	if errs != nil {
		t.Fatalf("%s: %v", query, errs)
	}
	return gqlcomplexity.Calculate(context.Background(), es, doc.Operations[0], vars)
}

func TestListCost(t *testing.T) {
	limit := func(n int) *int { return &n }
	for name, c := range map[string]struct{ got, want int }{
		"a list is itself plus its items": {listCost(3, 10), 31},
		"an empty selection still counts": {listCost(0, 10), 11},
		"saturates":                       {listCost(maxCost, 2), maxCost},
		"saturates before overflowing":    {listCost(listCost(listCost(maxCost/2, 500), 500), 500), maxCost},
		"no limit":                        {pageSize(nil, 50, 500), 50},
		"limit 0 is the default":          {pageSize(limit(0), 50, 500), 50},
		"negative is refused later":       {pageSize(limit(-4), 50, 500), 50},
		"as asked":                        {pageSize(limit(7), 50, 500), 7},
		"more than the service accepts":   {pageSize(limit(1<<40), 50, 500), 500},
	} {
		if c.got != c.want {
			t.Errorf("%s: %d, want %d", name, c.got, c.want)
		}
	}
}

// TestComplexityOfQueries pins the numbers the limit was chosen from. A rendition with two
// fields costs 2, its list 1 + 10 x 2 = 21, a version with two fields 23, the default page of
// 50 versions 1 + 50 x 23 = 1151, an asset with two fields 1153.
func TestComplexityOfQueries(t *testing.T) {
	const levels = `{ nodes { id title versions { nodes { id number renditions { id kind } } } } }`
	const oneLevels = `{ id title versions { nodes { id number renditions { id kind } } } }`
	// A file link costs 3, the 5 of a rendition 16, a rendition 17, a version 172, an asset 8602.
	const files = `{ nodes { id versions { nodes { id renditions { id files { role file { id } } } } } } }`
	const everything = `{ myLevel projects { project { id } } versions { nodes { renditions { kind
		files { file { id hashes { algo } copies { path location { baseUri } } } }
		subtitleTracks { id cues { nodes { text } } } } } } }`
	const tree = `{ id title description myLevel metadata legacyIds originalLanguage capturedAt camera reel createdAt updatedAt
		type { name }
		projects { role linkOrigin project { id code name } }
		versions { nodes { id number label status createdBy
			renditions { id kind language purpose
				files { role position file { id sizeBytes mimeType codec copies { path location { name } } } }
				subtitleTracks { id language kind revision } } } } }`
	for _, c := range []struct {
		name    string
		query   string
		vars    map[string]any
		want    int
		allowed bool
	}{
		{"a field", `{ ping }`, nil, 1, true},
		{"one asset with two levels", `{ asset(id: "AS-1") ` + oneLevels + ` }`, nil, 1154, true},
		{"a page of 100 assets with two levels", `{ assets(first: 100) ` + levels + ` }`, nil, 1 + 100*1153, true},
		{"the default page", `{ assets ` + levels + ` }`, nil, 1 + 50*1153, true},
		{"500 assets with two levels", `{ assets(first: 500) ` + levels + ` }`, nil, 1 + 500*1153, true},
		{"100 assets with three levels", `{ assets(first: 100) ` + files + ` }`, nil, 1 + 100*8602, true},
		{"500 assets with three levels", `{ assets(first: 500) ` + files + ` }`, nil, 1 + 500*8602, false},
		{"500 assets with three levels, 5 versions each", `{ assets(first: 500) { nodes { id versions(first: 5) { nodes { id renditions { id files { role file { id } } } } } } } }`, nil, 1 + 500*(1+1+5*172), true},
		{"one asset with everything down to its cues", `{ asset(id: "AS-1") ` + everything + ` }`, nil, 543560, true},
		{"the limit comes from a variable", `query($n: Int) { assets(first: $n) { nodes { id } } }`, map[string]any{"n": 20}, 21, true},
		{"one asset with its whole tree (yaak)", `{ asset(id: "AS-1") ` + tree + ` }`, nil, 49835, true},
		{"100 assets with their whole trees", `{ assets(first: 100) { nodes ` + tree + ` } }`, nil, 1 + 100*49834, false},
		{"a program page with projects", `{ programs { nodes { id projects { nodes { id code name status } } } } }`, nil, 1 + 50*(1+1+200*4), true},
		{"cues", `{ subtitleTrack(id: "ST-1") { cues(first: 5000) { nodes { seq text } } } }`, nil, 1 + 1 + 5000*2, true},
		{"cues without a limit and an export", `{ subtitleTrack(id: "ST-1") { cues { nodes { text } } export(format: SRT) } }`, nil, 1 + 501 + 500, true},
		{"history counts more tracks", `{ subtitleTracks(assetId: "AS-1", history: true) { id } }`, nil, 11, true},
		{"pageInfo adds its fields once per item", `{ assets(first: 100) { pageInfo { hasNextPage endCursor } nodes { id title versions { pageInfo { endCursor } nodes { id number renditions { id kind } } } } } }`, nil, 1 + 100*(2+1153+50*1), true},
		{"reference lists", `{ languages { code } assetTypes { id } userGroups { id } }`, nil, 201 + 21 + 101, true},
		{"mutations are counted the same way", `mutation { deleteAsset(id: "AS-1") }`, nil, 1, true},
	} {
		got := cost(t, c.query, c.vars)
		if got != c.want {
			t.Errorf("%s: costs %d, want %d", c.name, got, c.want)
		}
		if (got <= ComplexityLimit) != c.allowed {
			t.Errorf("%s: costs %d against a limit of %d, allowed should be %v", c.name, got, ComplexityLimit, c.allowed)
		}
	}
}

// Every list of objects in the schema must have a cost of its own; a new one without a line in
// complexity() would cost like a single object. A connection is such a list: its page size is
// an argument of the field that returns it, so that field carries the cost and the
// connection's own nodes and pageInfo must add nothing beyond what is selected in them.
func TestEveryListHasACost(t *testing.T) {
	es := generated.NewExecutableSchema(generated.Config{Resolvers: &Resolver{}, Complexity: complexity()})
	isConnection := func(name string) bool { return strings.HasSuffix(name, "Connection") }
	for _, def := range es.Schema().Types {
		if def.Kind != ast.Object || def.BuiltIn || def.Name == "Mutation" || def.Name == "Subscription" {
			continue
		}
		if isConnection(def.Name) {
			nodes, ok1 := es.Complexity(context.Background(), def.Name, "nodes", 7, nil)
			info, ok2 := es.Complexity(context.Background(), def.Name, "pageInfo", 2, nil)
			if !ok1 || !ok2 || nodes != 7 || info != 2 {
				t.Errorf("%s: nodes cost %d for a selection of 7 and pageInfo %d for one of 2; want 7 and 2", def.Name, nodes, info)
			}
			continue
		}
		for _, f := range def.Fields {
			elem := f.Type.Elem
			list := elem != nil && es.Schema().Types[elem.Name()].Kind == ast.Object
			if !list && !isConnection(f.Type.Name()) {
				continue
			}
			args := map[string]any{}
			for _, a := range f.Arguments {
				switch a.Type.Name() {
				case "Boolean":
					args[a.Name] = false
				case "String", "ID":
					args[a.Name] = ""
				}
			}
			if got, ok := es.Complexity(context.Background(), def.Name, f.Name, 7, args); !ok || got <= 8 {
				t.Errorf("%s.%s is a list of objects but costs %d for a selection of 7", def.Name, f.Name, got)
			}
		}
	}
}
