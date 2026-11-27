package vidispineimport

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/bcc-media/mediabrain/internal/asset"
	"github.com/bcc-media/mediabrain/internal/db"
	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/domain"
	"github.com/bcc-media/mediabrain/internal/file"
	"github.com/bcc-media/mediabrain/internal/subtitle"
	"github.com/bcc-media/mediabrain/pkg/vidispine"
)

// What an import takes besides the original, and how it records it.
const (
	// ShapeTagTranscript is the shape Mediabanken's transcription leaves on an item: an SRT file.
	ShapeTagTranscript = vidispine.ShapeTagTranscribedSRT
	// PreviewPurpose is the purpose of the encode rendition made from the lowres_watermarked shape.
	PreviewPurpose = "preview"
	// TranscriptSource is the source of the subtitle track made from the transcript.
	TranscriptSource = "Mediabanken"
	// TranscriptLanguage is the language of the transcript: Mediabanken transcribes Norwegian.
	TranscriptLanguage = "nb"
)

// plan is everything an import or refresh needs from Vidispine, read before the transaction
// opens so that no transaction waits on another system.
type plan struct {
	itemID   string
	meta     *vidispine.MetadataResult
	metadata json.RawMessage
	// original must be usable when the item has one. preview and transcript are extras: one
	// that cannot be used is left out (and logged) rather than holding up the import.
	original, preview, transcript *shapeFile
	transcriptText                string
	locations                     map[string]file.LocationInput
}

func (s *Service) read(ctx context.Context, itemID string) (*plan, error) {
	meta, err := s.src.GetMetadata(ctx, itemID)
	if err != nil {
		return nil, upstream(ctx, err, itemID)
	}
	shapes, err := s.src.GetShapes(ctx, itemID)
	if err != nil {
		return nil, upstream(ctx, err, itemID)
	}
	p := &plan{itemID: itemID, meta: meta, metadata: metadataOf(meta, itemID), locations: map[string]file.LocationInput{}}
	if p.original, err = originalOf(shapes); err != nil {
		return nil, err
	}
	p.preview = p.extra(ctx, shapes.GetShape(vidispine.ShapeTagLowresWatermarked), "preview")
	if p.transcript = p.extra(ctx, shapes.GetShape(ShapeTagTranscript), "transcript"); p.transcript != nil {
		if p.transcriptText, err = s.readTranscript(ctx, p.transcript, itemID); err != nil {
			return nil, err
		}
		if p.transcriptText == "" {
			p.transcript = nil
		}
	}

	for _, f := range []**shapeFile{&p.original, &p.preview, &p.transcript} {
		if *f == nil {
			continue
		}
		id := (*f).storageID
		if _, ok := p.locations[id]; ok {
			continue
		}
		storage, err := s.src.GetStorageMethods(ctx, id)
		if err != nil {
			slog.ErrorContext(ctx, "vidispine: storage request failed", "item", itemID, "storage", id, "error", err)
			return nil, domain.Unavailable("Vidispine did not answer")
		}
		loc, err := locationOf(id, storage)
		switch {
		case err == nil:
			p.locations[id] = loc
		case f == &p.original:
			return nil, err
		default:
			p.skip(ctx, *f, err)
			*f = nil
		}
	}
	return p, nil
}

func (p *plan) extra(ctx context.Context, shape *vidispine.Shape, what string) *shapeFile {
	f, err := fileOf(shape, what)
	if err != nil {
		slog.WarnContext(ctx, "vidispine: shape left out", "item", p.itemID, "shape", shape.ID, "what", what, "reason", err)
		return nil
	}
	return f
}

func (p *plan) skip(ctx context.Context, f *shapeFile, reason error) {
	slog.WarnContext(ctx, "vidispine: shape left out", "item", p.itemID, "shape", f.shapeID, "reason", reason)
}

// readTranscript returns the transcript's text, or "" when it is left out: missing, too large,
// not what Vidispine's hash says, or not SRT. Only a failure to reach Vidispine is an error.
func (s *Service) readTranscript(ctx context.Context, f *shapeFile, itemID string) (string, error) {
	data, err := s.src.ReadFile(ctx, f.fileID, subtitle.MaxContentBytes)
	leave := func(reason error) (string, error) {
		slog.WarnContext(ctx, "vidispine: shape left out", "item", itemID, "shape", f.shapeID, "what", "transcript", "reason", reason)
		return "", nil
	}
	switch {
	case errors.Is(err, vidispine.ErrTooLarge), errors.Is(err, vidispine.ErrNotFound):
		return leave(err)
	case err != nil:
		slog.ErrorContext(ctx, "vidispine: file request failed", "item", itemID, "file", f.fileID, "error", err)
		return "", domain.Unavailable("Vidispine did not answer")
	}
	if sum := sha1.Sum(data); hex.EncodeToString(sum[:]) != f.sha1 {
		return leave(errors.New("the content does not match Vidispine's SHA-1"))
	}
	text := string(data)
	if _, err := subtitle.Parse(subtitle.SRT, text); err != nil {
		return leave(err)
	}
	return text, nil
}

// apply adds to the asset what the plan has and the asset lacks, all in the version
// "Vidispine original": a master rendition with the original, an encode rendition with the
// preview, and the transcript as a subtitle revision unless a revision was already made from
// that very file.
func (p *plan) apply(ctx context.Context, q sqlc.Querier, a sqlc.Asset) error {
	if p.original == nil && p.preview == nil && p.transcript == nil {
		return nil
	}
	tx := db.SameTx(q)
	assets, files := asset.NewService(q, tx), file.NewService(q, tx)
	v, renditions, err := vidispineVersion(ctx, q, assets, a.ID)
	if err != nil {
		return err
	}
	register := func(f *shapeFile) (sqlc.File, error) {
		in := p.locations[f.storageID]
		loc, err := q.GetStorageLocationByName(ctx, in.Name)
		if errors.Is(domain.FromPG(err), domain.ErrNotFound) {
			loc, err = files.CreateLocation(ctx, in)
		}
		if err != nil {
			return sqlc.File{}, err
		}
		return files.Register(ctx, f.registerInput(loc.ID))
	}
	addRendition := func(f *shapeFile, kind sqlc.RenditionKind, purpose *string) error {
		r, err := assets.CreateRendition(ctx, v.ID, asset.RenditionInput{Kind: kind, Purpose: purpose, Metadata: f.renditionMetadata()})
		if err != nil {
			return err
		}
		registered, err := register(f)
		if err != nil {
			return err
		}
		_, err = assets.AttachFile(ctx, r.ID, registered.ID, f.role, 0)
		return err
	}

	if p.original != nil && !hasRendition(renditions, sqlc.RenditionKindMaster, "") {
		if err := addRendition(p.original, sqlc.RenditionKindMaster, nil); err != nil {
			return err
		}
	}
	if p.preview != nil && !hasRendition(renditions, sqlc.RenditionKindEncode, PreviewPurpose) {
		purpose := PreviewPurpose
		if err := addRendition(p.preview, sqlc.RenditionKindEncode, &purpose); err != nil {
			return err
		}
	}
	if p.transcript == nil {
		return nil
	}
	source, err := register(p.transcript)
	if err != nil {
		return err
	}
	tracks, err := q.ListAssetTracks(ctx, a.ID)
	if err != nil {
		return err
	}
	for _, t := range tracks {
		if t.SourceFileID != nil && *t.SourceFileID == source.ID {
			return nil
		}
	}
	language, kind, origin, from := TranscriptLanguage, sqlc.TrackKindSubtitle, sqlc.TrackOriginAsr, TranscriptSource
	_, err = subtitle.NewService(q, tx).Import(ctx, subtitle.ImportInput{
		VersionID: v.ID, Language: &language, Kind: &kind, Format: subtitle.SRT, Content: p.transcriptText,
		SourceFileID: &source.ID, Origin: &origin, Source: &from,
	})
	return err
}

// vidispineVersion returns the asset's version that the import made, creating it when there is
// none (a placeholder imported before it had files), with its renditions.
func vidispineVersion(ctx context.Context, q sqlc.Querier, assets *asset.Service, assetID string) (sqlc.AssetVersion, []sqlc.Rendition, error) {
	versions, err := q.ListAssetVersions(ctx, sqlc.ListAssetVersionsParams{AssetIds: []string{assetID}, Limit: domain.MaxLimit})
	if err != nil {
		return sqlc.AssetVersion{}, nil, err
	}
	for _, v := range versions {
		if v.Label != nil && *v.Label == versionLabel {
			renditions, err := q.ListRenditions(ctx, []string{v.ID})
			return v, renditions, err
		}
	}
	label := versionLabel
	v, err := assets.CreateVersion(ctx, assetID, asset.VersionInput{Label: &label})
	return v, nil, err
}

func hasRendition(rs []sqlc.Rendition, kind sqlc.RenditionKind, purpose string) bool {
	for _, r := range rs {
		if r.Kind == kind && (purpose == "" || r.Purpose != nil && *r.Purpose == purpose) {
			return true
		}
	}
	return false
}
