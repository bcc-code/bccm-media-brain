//go:build e2e

package e2e

import (
	"testing"

	"github.com/bcc-media/mediabrain/internal/fake"
	"github.com/bcc-media/mediabrain/internal/file"
)

// The helpers below create objects through GraphQL from internal/fake data and return ids.

type program struct{ ID, Code string }

func newProgram(t *testing.T, admin user, f *fake.Faker) program {
	t.Helper()
	in := f.Program()
	var out struct{ CreateProgram program }
	admin.must(t, `mutation($in: CreateProgramInput!) { createProgram(input: $in) { id code } }`,
		map[string]any{"in": map[string]any{"code": in.Code, "name": in.Name, "description": in.Description, "metadata": in.Metadata}}, &out)
	return out.CreateProgram
}

func projectVars(f *fake.Faker, p program) map[string]any {
	in := f.Project(p.ID, p.Code)
	return map[string]any{
		"programId": p.ID, "code": in.Code, "name": in.Name, "metadata": in.Metadata,
		"startsOn": in.StartsOn.Format("2006-01-02"), "endsOn": in.EndsOn.Format("2006-01-02"),
	}
}

func newProject(t *testing.T, u user, f *fake.Faker, p program) string {
	t.Helper()
	var out struct{ CreateProject struct{ ID string } }
	u.must(t, `mutation($in: CreateProjectInput!) { createProject(input: $in) { id } }`, map[string]any{"in": projectVars(f, p)}, &out)
	return out.CreateProject.ID
}

func assetVars(f *fake.Faker) map[string]any {
	in := f.Asset(1)
	return map[string]any{
		"typeId": in.TypeID, "title": in.Title, "description": in.Description, "capturedAt": in.CapturedAt,
		"camera": in.Camera, "reel": in.Reel, "metadata": in.Metadata, "legacyIds": in.LegacyIDs, "originalLanguage": "en",
	}
}

func newAsset(t *testing.T, u user, f *fake.Faker, projectID string) string {
	t.Helper()
	var out struct{ CreateAsset struct{ ID string } }
	vars := map[string]any{"in": assetVars(f)}
	if projectID != "" {
		vars["p"] = projectID
	}
	u.must(t, `mutation($p: ID, $in: AssetInput!) { createAsset(projectId: $p, input: $in) { id } }`, vars, &out)
	return out.CreateAsset.ID
}

func newVersion(t *testing.T, u user, assetID string) string {
	t.Helper()
	var out struct{ CreateAssetVersion struct{ ID string } }
	u.must(t, `mutation($a: ID!) { createAssetVersion(assetId: $a, input: {label: "Cut"}) { id } }`, map[string]any{"a": assetID}, &out)
	return out.CreateAssetVersion.ID
}

func newRendition(t *testing.T, u user, versionID string) string {
	t.Helper()
	var out struct{ CreateRendition struct{ ID string } }
	u.must(t, `mutation($v: ID!) { createRendition(versionId: $v, input: {kind: MASTER, language: "en"}) { id } }`, map[string]any{"v": versionID}, &out)
	return out.CreateRendition.ID
}

func newLocation(t *testing.T, admin user, f *fake.Faker) string {
	t.Helper()
	in := f.Location()
	var out struct{ CreateStorageLocation struct{ ID string } }
	admin.must(t, `mutation($in: StorageLocationInput!) { createStorageLocation(input: $in) { id } }`,
		map[string]any{"in": map[string]any{"name": in.Name, "kind": "NAS", "baseUri": in.BaseURI, "online": true}}, &out)
	return out.CreateStorageLocation.ID
}

var algoNames = map[string]string{"xxh3_128": "XXH3_128", "blake3": "BLAKE3", "sha256": "SHA256", "md5": "MD5"}

func fileVars(in file.RegisterInput, copies ...map[string]any) map[string]any {
	hashes := []map[string]any{}
	for _, h := range in.Hashes {
		hashes = append(hashes, map[string]any{"algo": algoNames[string(h.Algo)], "value": h.Value})
	}
	return map[string]any{
		"sizeBytes": in.SizeBytes, "mimeType": in.MimeType, "durationMs": in.DurationMs, "codec": in.Codec,
		"width": in.Width, "height": in.Height, "probe": in.Probe, "hashes": hashes, "copies": copies,
	}
}

const registerFile = `mutation($in: RegisterFileInput!) { registerFile(input: $in) { id sizeBytes } }`

// newFile registers a video file with one copy at the location (if given).
func newFile(t *testing.T, admin user, f *fake.Faker, locationID string) string {
	t.Helper()
	var copies []map[string]any
	if locationID != "" {
		copies = append(copies, map[string]any{"locationId": locationID, "path": f.Path("mov")})
	}
	var out struct{ RegisterFile struct{ ID string } }
	admin.must(t, registerFile, map[string]any{"in": fileVars(f.VideoFile(), copies...)}, &out)
	return out.RegisterFile.ID
}

func attach(t *testing.T, u user, renditionID, fileID string) {
	t.Helper()
	u.must(t, `mutation($r: ID!, $f: ID!) { attachFile(renditionId: $r, fileId: $f, role: VIDEO) { position } }`,
		map[string]any{"r": renditionID, "f": fileID}, nil)
}

const grantAccess = `mutation($in: GrantInput!) { grantAccess(input: $in) { id level scope scopeId userId groupId grantedBy expiresAt } }`

// grant gives access and returns the grant id. target is {"userId": ...}, {"userEmail": ...} or {"groupId": ...}.
func grant(t *testing.T, by user, scope, scopeID, level string, target map[string]any) string {
	t.Helper()
	in := map[string]any{"scope": scope, "scopeId": scopeID, "level": level}
	for k, v := range target {
		in[k] = v
	}
	var out struct{ GrantAccess struct{ ID string } }
	by.must(t, grantAccess, map[string]any{"in": in}, &out)
	return out.GrantAccess.ID
}

func to(u user) map[string]any { return map[string]any{"userId": u.id} }

// clip is an asset with one version, a master rendition and an attached video file.
type clip struct{ asset, version, rendition, file string }

func newClip(t *testing.T, admin user, f *fake.Faker, projectID, locationID string) clip {
	t.Helper()
	c := clip{asset: newAsset(t, admin, f, projectID)}
	c.version = newVersion(t, admin, c.asset)
	c.rendition = newRendition(t, admin, c.version)
	c.file = newFile(t, admin, f, locationID)
	attach(t, admin, c.rendition, c.file)
	return c
}

type fakeHash = file.HashInput
