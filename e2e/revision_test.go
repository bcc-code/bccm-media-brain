//go:build e2e

package e2e

import (
	"context"
	"sync"
	"testing"
)

// revised is one object with a full-replacement update: its table, its id, the mutation
// (taking $id and $rev) and the input the mutation is given.
type revised struct {
	table, id, mutation string
	input               func() map[string]any
}

func revisedObjects(w world) map[string]revised {
	fixed := func(in map[string]any) func() map[string]any { return func() map[string]any { return in } }
	return map[string]revised{
		"program": {"program", w.prog.ID,
			`mutation($id: ID!, $in: UpdateProgramInput!, $rev: Int) { updateProgram(id: $id, input: $in, expectedRevision: $rev) { revision } }`,
			fixed(map[string]any{"name": "Renamed"})},
		"project": {"project", w.projectA,
			`mutation($id: ID!, $in: UpdateProjectInput!, $rev: Int) { updateProject(id: $id, input: $in, expectedRevision: $rev) { revision } }`,
			fixed(map[string]any{"name": "Renamed", "status": "ACTIVE"})},
		"asset": {"asset", w.c1.asset,
			`mutation($id: ID!, $in: AssetInput!, $rev: Int) { updateAsset(id: $id, input: $in, expectedRevision: $rev) { revision } }`,
			fixed(map[string]any{"typeId": 1, "title": "Renamed"})},
		"version": {"asset_version", w.c1.version,
			`mutation($id: ID!, $in: VersionInput!, $rev: Int) { updateAssetVersion(id: $id, input: $in, expectedRevision: $rev) { revision } }`,
			fixed(map[string]any{"label": "Renamed", "status": "REVIEW"})},
		"rendition": {"rendition", w.c1.rendition,
			`mutation($id: ID!, $in: UpdateRenditionInput!, $rev: Int) { updateRendition(id: $id, input: $in, expectedRevision: $rev) { revision } }`,
			fixed(map[string]any{"purpose": "renamed"})},
		"location": {"storage_location", w.location,
			`mutation($id: ID!, $in: StorageLocationInput!, $rev: Int) { updateStorageLocation(id: $id, input: $in, expectedRevision: $rev) { revision } }`,
			// Location names are unique, so every attempt brings its own.
			func() map[string]any {
				return map[string]any{"name": "renamed " + unique(), "kind": "NAS", "baseUri": "smb://renamed"}
			}},
	}
}

// row is the object as stored, to see that a refused update changed nothing.
func (r revised) row(t *testing.T) (revision int, content string) {
	t.Helper()
	err := pool.QueryRow(context.Background(), `SELECT revision, to_jsonb(x)::text FROM `+r.table+` x WHERE id = $1`, r.id).Scan(&revision, &content)
	if err != nil {
		t.Fatal(err)
	}
	return
}

func (r revised) vars(expected any) map[string]any {
	return map[string]any{"id": r.id, "in": r.input(), "rev": expected}
}

// update returns the revision the mutation answered with.
func (r revised) update(t *testing.T, u user, expected any) int {
	t.Helper()
	var out map[string]struct{ Revision int }
	u.must(t, r.mutation, r.vars(expected), &out)
	for _, o := range out {
		return o.Revision
	}
	t.Fatal("no object in the answer")
	return 0
}

func TestUpdatesCheckTheExpectedRevision(t *testing.T) {
	w := newWorld(t, 41)
	for name, r := range revisedObjects(w) {
		t.Run(name, func(t *testing.T) {
			start, _ := r.row(t)

			// Without an expected revision the last write wins, as before.
			if got := r.update(t, w.admin, nil); got != start+1 {
				t.Fatalf("update without expectedRevision answered revision %d, want %d", got, start+1)
			}
			// Based on the current revision: accepted, and the answer carries the next one.
			if got := r.update(t, w.admin, start+1); got != start+2 {
				t.Fatalf("update on revision %d answered revision %d, want %d", start+1, got, start+2)
			}

			// Based on an older revision: refused, and nothing changes.
			_, before := r.row(t)
			e := w.admin.fails(t, "CONFLICT", r.mutation, r.vars(start+1))
			if !contains(e.Message, "revision") {
				t.Errorf("the conflict should say what happened, got %q", e.Message)
			}
			if x := e.Extensions; x.Reason != "STALE_REVISION" || x.CurrentRevision == nil || *x.CurrentRevision != start+2 {
				t.Errorf("a client cannot tell that it must reload revision %d: %+v", start+2, x)
			}
			if now, after := r.row(t); now != start+2 || after != before {
				t.Fatalf("a refused update changed the row:\n%s\n%s", before, after)
			}

			for _, bad := range []int{0, -1} {
				if e := w.admin.fails(t, "INVALID_INPUT", r.mutation, r.vars(bad)); e.Extensions.Field != "expectedRevision" {
					t.Errorf("revision %d: the error names the field %q", bad, e.Extensions.Field)
				}
			}
		})
	}

	// An object that is gone is not found, whatever revision the caller expected.
	w.admin.fails(t, "NOT_FOUND", revisedObjects(w)["location"].mutation,
		map[string]any{"id": "SL-00000000000000000000000000", "in": revisedObjects(w)["location"].input(), "rev": 1})
}

// Two edits based on the same revision, sent at the same time: one is accepted.
func TestConcurrentUpdatesOnOneRevision(t *testing.T) {
	w := newWorld(t, 42)
	const editors = 8
	for name, r := range revisedObjects(w) {
		t.Run(name, func(t *testing.T) {
			start, _ := r.row(t)
			codes := make([]string, editors)
			var ready, done sync.WaitGroup
			release := make(chan struct{})
			for i := range editors {
				ready.Add(1)
				done.Add(1)
				vars := r.vars(start)
				go func() {
					defer done.Done()
					ready.Done()
					<-release
					_, resp := post(t, map[string]string{"Authorization": "Bearer " + w.admin.token}, r.mutation, vars)
					if len(resp.Errors) > 0 {
						codes[i] = resp.Errors[0].Extensions.Code
					}
				}()
			}
			ready.Wait()
			close(release)
			done.Wait()

			accepted, refused := 0, 0
			for _, code := range codes {
				switch code {
				case "":
					accepted++
				case "CONFLICT":
					refused++
				}
			}
			if now, _ := r.row(t); accepted != 1 || refused != editors-1 || now != start+1 {
				t.Fatalf("%d accepted, %d refused (%v), revision %d -> %d; want exactly one accepted", accepted, refused, codes, start, now)
			}
		})
	}
}

// Only edits of the object's own fields count: links and children leave its revision alone,
// and the Vidispine refresh, which rewrites metadata, counts as an edit.
func TestRevisionCountsOwnEditsOnly(t *testing.T) {
	w := newWorld(t, 43)
	asset := revisedObjects(w)["asset"]
	start, _ := asset.row(t)
	w.admin.must(t, `mutation($a: ID!, $p: ID!) { linkAssetToProject(assetId: $a, projectId: $p, role: REFERENCE) { role } }`,
		map[string]any{"a": w.c1.asset, "p": w.projectB}, nil)
	newVersion(t, w.admin, w.c1.asset)
	if now, _ := asset.row(t); now != start {
		t.Fatalf("a link and a new version moved the asset from revision %d to %d", start, now)
	}
	var read struct{ Asset struct{ Revision int } }
	w.admin.must(t, `query($id: ID!) { asset(id: $id) { revision } }`, map[string]any{"id": w.c1.asset}, &read)
	if read.Asset.Revision != start {
		t.Fatalf("asset.revision = %d, the row has %d", read.Asset.Revision, start)
	}

	item := vidispineItem(w.f, newVidispineStorage())
	imported := importItem(t, w.admin, item.ID, w.projectA)
	w.admin.must(t, refreshFromVidispine, map[string]any{"id": imported.ID}, nil)
	var revision int
	if err := pool.QueryRow(context.Background(), `SELECT revision FROM asset WHERE id = $1`, imported.ID).Scan(&revision); err != nil || revision != 2 {
		t.Fatalf("an imported and refreshed asset is at revision %d, want 2 (%v)", revision, err)
	}
}

// The database counts revisions and keeps updated_at, so a statement that says nothing about
// either (a new query, a fix by hand) still makes an update based on the old row stale.
func TestTheDatabaseCountsRevisions(t *testing.T) {
	w := newWorld(t, 44)
	ctx := context.Background()
	for name, r := range revisedObjects(w) {
		t.Run(name, func(t *testing.T) {
			var start, now int
			var moved bool
			err := pool.QueryRow(ctx, `SELECT revision FROM `+r.table+` WHERE id = $1`, r.id).Scan(&start)
			if err != nil {
				t.Fatal(err)
			}
			err = pool.QueryRow(ctx, `UPDATE `+r.table+` x SET id = x.id FROM `+r.table+` old
				WHERE x.id = $1 AND old.id = x.id RETURNING x.revision, x.updated_at > old.updated_at`, r.id).Scan(&now, &moved)
			if err != nil || now != start+1 || !moved {
				t.Fatalf("an UPDATE that names neither column: revision %d -> %d, updated_at moved: %v (%v)", start, now, moved, err)
			}
			if e := w.admin.fails(t, "CONFLICT", r.mutation, r.vars(start)); !contains(e.Message, "revision") {
				t.Fatalf("an update based on revision %d was refused with %q", start, e.Message)
			}
		})
	}

	var read struct {
		Asset struct {
			Versions []struct {
				UpdatedAt  string
				Renditions []struct{ UpdatedAt string }
			}
		}
		StorageLocations []struct{ UpdatedAt string }
	}
	w.admin.must(t, `query($id: ID!) { asset(id: $id) { versions { updatedAt renditions { updatedAt } } } storageLocations { updatedAt } }`,
		map[string]any{"id": w.c1.asset}, &read)
	if len(read.StorageLocations) == 0 || read.StorageLocations[0].UpdatedAt == "" ||
		len(read.Asset.Versions) == 0 || read.Asset.Versions[0].UpdatedAt == "" ||
		len(read.Asset.Versions[0].Renditions) == 0 || read.Asset.Versions[0].Renditions[0].UpdatedAt == "" {
		t.Fatalf("updatedAt is missing: %+v", read)
	}
}
