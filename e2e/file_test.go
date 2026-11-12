//go:build e2e

package e2e

import (
	"strings"
	"testing"

	"github.com/bcc-media/mediabrain/internal/fake"
)

const fileDetails = `query($id: ID!) { file(id: $id) {
	id sizeBytes mimeType durationMs codec width height probe
	hashes { algo value }
	copies { id path status lastSeenAt lastVerifiedAt location { id name baseUri } }
} }`

type fileJSON struct {
	ID        string
	SizeBytes int64
	Codec     *string
	Width     *int
	Probe     map[string]any
	Hashes    []struct{ Algo, Value string }
	Copies    []struct {
		ID, Path, Status           string
		LastSeenAt, LastVerifiedAt *string
		Location                   struct{ ID, Name string }
	}
}

func TestStorageLocations(t *testing.T) {
	admin, f := signInAdmin(t), fake.New(3)
	const create = `mutation($in: StorageLocationInput!) { createStorageLocation(input: $in) { id name kind baseUri online } }`
	in := f.Location()
	vars := map[string]any{"in": map[string]any{"name": in.Name, "kind": "S3", "baseUri": in.BaseURI}}
	var out struct {
		CreateStorageLocation struct {
			ID, Name, Kind, BaseUri string
			Online                  bool
		}
	}
	admin.must(t, create, vars, &out)
	loc := out.CreateStorageLocation
	if loc.Name != in.Name || loc.Kind != "S3" || !loc.Online {
		t.Fatalf("location = %+v", loc)
	}
	admin.fails(t, "CONFLICT", create, vars)
	admin.fails(t, "INVALID_INPUT", create, map[string]any{"in": map[string]any{"name": " ", "kind": "S3", "baseUri": "s3://x"}})
	admin.fails(t, "INVALID_INPUT", create, map[string]any{"in": map[string]any{"name": "x", "kind": "S3", "baseUri": ""}})

	const update = `mutation($id: ID!, $in: StorageLocationInput!) { updateStorageLocation(id: $id, input: $in) { name kind online } }`
	var up struct {
		UpdateStorageLocation struct {
			Name, Kind string
			Online     bool
		}
	}
	admin.must(t, update, map[string]any{"id": loc.ID, "in": map[string]any{"name": in.Name + "-b", "kind": "LTO", "baseUri": "lto://b", "online": false}}, &up)
	if up.UpdateStorageLocation.Kind != "LTO" || up.UpdateStorageLocation.Online {
		t.Fatalf("updated = %+v", up.UpdateStorageLocation)
	}
	admin.fails(t, "NOT_FOUND", update, map[string]any{"id": "SL-00000000000000000000000000", "in": map[string]any{"name": "n", "kind": "NAS", "baseUri": "u"}})
	admin.fails(t, "INVALID_INPUT", update, map[string]any{"id": loc.ID, "in": map[string]any{"name": "", "kind": "NAS", "baseUri": "u"}})

	var list struct{ StorageLocations []struct{ ID string } }
	admin.must(t, `{ storageLocations { id } }`, nil, &list)
	if len(list.StorageLocations) == 0 {
		t.Fatal("no locations listed")
	}

	// A location that holds a copy cannot be deleted.
	file := newFile(t, admin, f, loc.ID)
	const del = `mutation($id: ID!) { deleteStorageLocation(id: $id) }`
	admin.fails(t, "CONFLICT", del, map[string]any{"id": loc.ID})
	var d fileJSON
	admin.must(t, fileDetails, map[string]any{"id": file}, &struct{ File *fileJSON }{&d})
	admin.must(t, `mutation($id: ID!) { removeFileCopy(id: $id) }`, map[string]any{"id": d.Copies[0].ID}, nil)
	admin.must(t, del, map[string]any{"id": loc.ID}, nil)
	admin.fails(t, "NOT_FOUND", del, map[string]any{"id": loc.ID})
}

func TestRegisterFile(t *testing.T) {
	admin, f := signInAdmin(t), fake.New(4)
	locA, locB := newLocation(t, admin, f), newLocation(t, admin, f)

	video := f.VideoFile()
	pathA := f.Path("mov")
	var reg struct {
		RegisterFile struct {
			ID        string
			SizeBytes int64
		}
	}
	admin.must(t, registerFile, map[string]any{"in": fileVars(video, map[string]any{"locationId": locA, "path": pathA})}, &reg)
	id := reg.RegisterFile.ID
	if !strings.HasPrefix(id, "FI-") || reg.RegisterFile.SizeBytes != video.SizeBytes {
		t.Fatalf("registered = %+v", reg.RegisterFile)
	}

	var got fileJSON
	admin.must(t, fileDetails, map[string]any{"id": id}, &struct{ File *fileJSON }{&got})
	if *got.Codec != *video.Codec || *got.Width != int(*video.Width) || got.Probe["streams"] == nil ||
		len(got.Hashes) != 2 || got.Hashes[0].Value != video.Hashes[0].Value || got.Hashes[0].Algo != "XXH3_128" {
		t.Fatalf("file = %+v", got)
	}
	if len(got.Copies) != 1 || got.Copies[0].Path != pathA || got.Copies[0].Status != "PRESENT" ||
		got.Copies[0].LastSeenAt == nil || got.Copies[0].Location.ID != locA {
		t.Fatalf("copies = %+v", got.Copies)
	}

	// The same content seen elsewhere: one known hash is enough to find the file. The new
	// hash and the new copy are added to it; the existing copy is not duplicated.
	again := video
	again.Hashes = append([]fakeHash{video.Hashes[1]}, fakeHash{Algo: "md5", Value: strings.Repeat("ab", 16)})
	admin.must(t, registerFile, map[string]any{"in": fileVars(again,
		map[string]any{"locationId": locA, "path": pathA}, map[string]any{"locationId": locB, "path": "copy/of/it.mov"})}, &reg)
	if reg.RegisterFile.ID != id {
		t.Fatalf("same content registered as a new file %s", reg.RegisterFile.ID)
	}
	admin.must(t, fileDetails, map[string]any{"id": id}, &struct{ File *fileJSON }{&got})
	if len(got.Hashes) != 3 || len(got.Copies) != 2 {
		t.Fatalf("after second registration: %d hashes, %d copies", len(got.Hashes), len(got.Copies))
	}

	var byHash struct{ FileByHash *struct{ ID string } }
	admin.must(t, `query($h: FileHashInput!) { fileByHash(hash: $h) { id } }`,
		map[string]any{"h": map[string]any{"algo": "MD5", "value": strings.Repeat("AB", 16)}}, &byHash)
	if byHash.FileByHash == nil || byHash.FileByHash.ID != id {
		t.Fatalf("by hash = %+v", byHash.FileByHash)
	}
	admin.must(t, `query($h: FileHashInput!) { fileByHash(hash: $h) { id } }`,
		map[string]any{"h": map[string]any{"algo": "MD5", "value": strings.Repeat("00", 16)}}, &byHash)
	if byHash.FileByHash != nil {
		t.Fatal("unknown hash found a file")
	}

	t.Run("conflicts", func(t *testing.T) {
		other := newFile(t, admin, f, "")
		var o fileJSON
		admin.must(t, fileDetails, map[string]any{"id": other}, &struct{ File *fileJSON }{&o})

		wrongSize := video
		wrongSize.SizeBytes++
		admin.fails(t, "CONFLICT", registerFile, map[string]any{"in": fileVars(wrongSize)})

		// One hash of each file.
		mixed := video
		mixed.Hashes = []fakeHash{video.Hashes[0], {Algo: "sha256", Value: o.Hashes[1].Value}}
		admin.fails(t, "CONFLICT", registerFile, map[string]any{"in": fileVars(mixed)})

		// A known hash together with a different value for an algorithm the file already has.
		changed := video
		changed.Hashes = []fakeHash{video.Hashes[0], {Algo: "sha256", Value: strings.Repeat("0", 64)}}
		admin.fails(t, "CONFLICT", registerFile, map[string]any{"in": fileVars(changed)})

		// A path that already holds another file.
		admin.fails(t, "CONFLICT", registerFile, map[string]any{"in": fileVars(f.VideoFile(), map[string]any{"locationId": locA, "path": pathA})})
	})

	t.Run("invalid", func(t *testing.T) {
		patch := func(change func(in map[string]any)) map[string]any {
			in := fileVars(f.VideoFile())
			change(in)
			return map[string]any{"in": in}
		}
		hash := func(algo, value string) []map[string]any { return []map[string]any{{"algo": algo, "value": value}} }
		for name, vars := range map[string]map[string]any{
			"negative size":     patch(func(in map[string]any) { in["sizeBytes"] = -1 }),
			"negative duration": patch(func(in map[string]any) { in["durationMs"] = -1 }),
			"zero width":        patch(func(in map[string]any) { in["width"] = 0 }),
			"no hashes":         patch(func(in map[string]any) { in["hashes"] = []any{} }),
			"short hash":        patch(func(in map[string]any) { in["hashes"] = hash("SHA256", "abcd") }),
			"not hex":           patch(func(in map[string]any) { in["hashes"] = hash("MD5", strings.Repeat("zz", 16)) }),
			"same algo twice": patch(func(in map[string]any) {
				in["hashes"] = append(hash("MD5", strings.Repeat("aa", 16)), hash("MD5", strings.Repeat("bb", 16))...)
			}),
			"probe array":   patch(func(in map[string]any) { in["probe"] = []int{1} }),
			"absolute path": patch(func(in map[string]any) { in["copies"] = []map[string]any{{"locationId": locA, "path": "/etc/passwd"}} }),
			"parent path":   patch(func(in map[string]any) { in["copies"] = []map[string]any{{"locationId": locA, "path": "a/../../b"}} }),
			"empty path":    patch(func(in map[string]any) { in["copies"] = []map[string]any{{"locationId": locA, "path": " "}} }),
			"no location": patch(func(in map[string]any) {
				in["copies"] = []map[string]any{{"locationId": "SL-00000000000000000000000000", "path": "a.mov"}}
			}),
		} {
			t.Run(name, func(t *testing.T) { admin.fails(t, "INVALID_INPUT", registerFile, vars) })
		}
		admin.fails(t, "INVALID_INPUT", `{ fileByHash(hash: {algo: MD5, value: "xyz"}) { id } }`, nil)
		admin.fails(t, "INVALID_INPUT", `{ files(first: 9999) { nodes { id } } }`, nil)
	})

	// The size of a large file survives as a 64-bit number.
	big := f.VideoFile()
	big.SizeBytes = 1 << 40
	admin.must(t, registerFile, map[string]any{"in": fileVars(big)}, &reg)
	if reg.RegisterFile.SizeBytes != 1<<40 {
		t.Fatalf("large size = %d", reg.RegisterFile.SizeBytes)
	}
	var list struct{ Files nodes[struct{ ID string }] }
	admin.must(t, `{ files(first: 2) { nodes { id } } }`, nil, &list)
	if len(list.Files) != 2 || list.Files[0].ID != reg.RegisterFile.ID {
		t.Fatalf("files are listed newest first, got %+v", list.Files)
	}
}

func TestFileCopies(t *testing.T) {
	admin, f := signInAdmin(t), fake.New(5)
	loc := newLocation(t, admin, f)
	file := newFile(t, admin, f, "")

	const add = `mutation($f: ID!, $c: FileCopyInput!) { addFileCopy(fileId: $f, copy: $c) { id status lastSeenAt lastVerifiedAt } }`
	type copyJSON struct {
		ID, Status                 string
		LastSeenAt, LastVerifiedAt *string
	}
	var added struct{ AddFileCopy copyJSON }
	vars := map[string]any{"f": file, "c": map[string]any{"locationId": loc, "path": "a/b.mov"}}
	admin.must(t, add, vars, &added)
	c := added.AddFileCopy
	if c.Status != "PRESENT" || c.LastSeenAt == nil || c.LastVerifiedAt != nil {
		t.Fatalf("copy = %+v", c)
	}
	// Adding the same copy again is not an error and not a second row.
	admin.must(t, add, vars, &added)
	if added.AddFileCopy.ID != c.ID {
		t.Fatal("same path created a second copy")
	}
	admin.fails(t, "CONFLICT", add, map[string]any{"f": newFile(t, admin, f, ""), "c": vars["c"]})
	admin.fails(t, "INVALID_INPUT", add, map[string]any{"f": file, "c": map[string]any{"locationId": loc, "path": "../x"}})
	admin.fails(t, "NOT_FOUND", add, map[string]any{"f": "FI-00000000000000000000000000", "c": vars["c"]})

	const set = `mutation($id: ID!, $s: CopyStatus!) { setFileCopyStatus(id: $id, status: $s) { id status lastSeenAt lastVerifiedAt } }`
	var status struct{ SetFileCopyStatus copyJSON }
	admin.must(t, set, map[string]any{"id": c.ID, "s": "MISSING"}, &status)
	if s := status.SetFileCopyStatus; s.Status != "MISSING" || *s.LastSeenAt != *c.LastSeenAt || s.LastVerifiedAt == nil {
		t.Fatalf("missing: a missing copy was not seen, got %+v (was %+v)", s, c)
	}
	admin.must(t, set, map[string]any{"id": c.ID, "s": "MISMATCH"}, &status)
	if s := status.SetFileCopyStatus; s.Status != "MISMATCH" || *s.LastSeenAt == *c.LastSeenAt {
		t.Fatalf("mismatch: the copy was seen, got %+v", s)
	}
	admin.fails(t, "NOT_FOUND", set, map[string]any{"id": "FC-00000000000000000000000000", "s": "PRESENT"})

	// A file with copies cannot be deleted; without copies it can.
	const del = `mutation($id: ID!) { deleteFile(id: $id) }`
	admin.fails(t, "CONFLICT", del, map[string]any{"id": file})
	admin.must(t, `mutation($id: ID!) { removeFileCopy(id: $id) }`, map[string]any{"id": c.ID}, nil)
	admin.fails(t, "NOT_FOUND", `mutation($id: ID!) { removeFileCopy(id: $id) }`, map[string]any{"id": c.ID})
	admin.must(t, del, map[string]any{"id": file}, nil)
	admin.isNull(t, "file", file)
	admin.fails(t, "NOT_FOUND", del, map[string]any{"id": file})
}
