package cantemo

import (
	"encoding/hex"
	"encoding/json"
	"math"
	"net/url"
	"strings"

	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/domain"
	"github.com/bcc-media/mediabrain/internal/file"
	"github.com/bcc-media/mediabrain/pkg/vidispine"
)

func titleOf(m *vidispine.MetadataResult, itemID string) string {
	values := m.ItemValues()
	for _, field := range []string{vidispine.FieldTitle, vidispine.FieldOriginalFilename} {
		for _, v := range values[field] {
			if t := strings.TrimSpace(v); t != "" {
				return t
			}
		}
	}
	return itemID
}

// original is the file of an item's original shape, as MediaBrain records it.
type original struct {
	shapeID, fileID string
	storageID, path string
	sha1            string
	size            int64
	mimeType, codec *string
	durationMs      *int64
	width, height   *int32
	role            sqlc.FileRole
}

// originalOf returns nil for an item without an original file, such as a placeholder.
func originalOf(shapes *vidispine.ShapeResult) (*original, error) {
	shape := shapes.GetShape(vidispine.ShapeTagOriginal)
	if shape == nil || shape.File() == nil {
		return nil, nil
	}
	f := shape.File()
	if f.State != vidispine.FileStateClosed {
		return nil, domain.Invalid("the original file is still being written in Cantemo (state %q)", f.State)
	}
	hash := strings.ToLower(strings.TrimSpace(f.Hash))
	if raw, err := hex.DecodeString(hash); err != nil || len(raw) != 20 {
		return nil, domain.Invalid("Cantemo has no SHA-1 hash for the original file yet")
	}
	path := strings.TrimLeft(f.Path, "/")
	if f.Storage == "" || path == "" {
		return nil, domain.Invalid("Cantemo does not say where the original file is stored")
	}
	o := &original{
		shapeID: shape.ID, fileID: f.ID, storageID: f.Storage, path: path, sha1: hash, size: f.Size,
		role: sqlc.FileRoleSidecar,
	}
	if len(shape.MimeType) > 0 && shape.MimeType[0] != "" {
		o.mimeType = &shape.MimeType[0]
	}
	if secs, ok := shape.ContainerComponent.Duration.Seconds(); ok && secs >= 0 {
		ms := int64(math.Round(secs * 1000))
		o.durationMs = &ms
	}
	switch {
	case len(shape.VideoComponent) > 0:
		v := shape.VideoComponent[0]
		o.role, o.codec = sqlc.FileRoleVideo, nonEmpty(v.Codec)
		if v.Resolution.Width > 0 && v.Resolution.Height > 0 {
			w, h := int32(v.Resolution.Width), int32(v.Resolution.Height)
			o.width, o.height = &w, &h
		}
	case len(shape.AudioComponent) > 0:
		o.role, o.codec = sqlc.FileRoleAudio, nonEmpty(shape.AudioComponent[0].Codec)
	}
	return o, nil
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (o *original) registerInput(locationID string) file.RegisterInput {
	return file.RegisterInput{
		SizeBytes: o.size, MimeType: o.mimeType, DurationMs: o.durationMs, Codec: o.codec, Width: o.width, Height: o.height,
		Hashes: []file.HashInput{{Algo: sqlc.HashAlgoSha1, Value: o.sha1}},
		Copies: []file.CopyInput{{LocationID: locationID, Path: o.path}},
	}
}

func (o *original) renditionMetadata() json.RawMessage {
	raw, _ := json.Marshal(map[string]map[string]string{MetadataKey: {"shapeId": o.shapeID, "fileId": o.fileID}})
	return raw
}

// locationOf describes the storage location for a Vidispine storage. It is named after the
// storage id, which is how later imports find it again.
func locationOf(storageID string, storage *vidispine.StorageResult) (file.LocationInput, error) {
	var uris []*url.URL
	for _, m := range storage.Methods {
		u, err := url.Parse(m.URI)
		if err != nil || u.Scheme == "" {
			continue
		}
		// Vidispine keeps access keys and passwords in the URI. They must not end up in a
		// field every administrator can read.
		u.User = nil
		if u.Scheme == "file" {
			uris = append([]*url.URL{u}, uris...)
		} else {
			uris = append(uris, u)
		}
	}
	if len(uris) == 0 {
		return file.LocationInput{}, domain.Invalid("Cantemo storage %s has no address", storageID)
	}
	kind := sqlc.LocationKindNas
	if uris[0].Scheme == "s3" {
		kind = sqlc.LocationKindS3
	}
	return file.LocationInput{Name: storageID, Kind: kind, BaseURI: uris[0].String(), Online: true}, nil
}
