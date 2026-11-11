// Package vidispineimport imports single items from Vidispine as assets and refreshes
// the Vidispine metadata kept on them.
package vidispineimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/bcc-media/mediabrain/internal/access"
	"github.com/bcc-media/mediabrain/internal/asset"
	"github.com/bcc-media/mediabrain/internal/db"
	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/domain"
	"github.com/bcc-media/mediabrain/internal/file"
	"github.com/bcc-media/mediabrain/pkg/vidispine"
)

// MetadataKey is the key of asset.metadata that holds what came from Vidispine. Refresh replaces
// this key and nothing else.
const MetadataKey = "vidispine"

const versionLabel = "Vidispine original"

var itemIDPattern = regexp.MustCompile(`^VX-[0-9]+$`)

// Source is the part of the Vidispine API the import reads. *vidispine.Client implements it.
type Source interface {
	GetMetadata(ctx context.Context, itemID string) (*vidispine.MetadataResult, error)
	GetShapes(ctx context.Context, itemID string) (*vidispine.ShapeResult, error)
	GetStorageMethods(ctx context.Context, storageID string) (*vidispine.StorageResult, error)
}

// NewSource returns a client for the Vidispine API at baseURL, or nil when baseURL is empty:
// Vidispine is then not configured.
func NewSource(baseURL, username, password string) (Source, error) {
	if baseURL == "" {
		return nil, nil
	}
	return vidispine.New(vidispine.Config{BaseURL: baseURL, Username: username, Password: password})
}

type Service struct {
	q   sqlc.Querier
	tx  db.TxRunner
	src Source
}

// NewService takes a nil src when Vidispine is not configured; every operation then fails with
// domain.ErrUnavailable.
func NewService(q sqlc.Querier, tx db.TxRunner, src Source) *Service {
	return &Service{q: q, tx: tx, src: src}
}

type ImportInput struct {
	ItemID    string
	TypeID    int16
	ProjectID *string
}

// Metadata is what is stored under MetadataKey.
type Metadata struct {
	ItemID      string              `json:"itemId"`
	RefreshedAt time.Time           `json:"refreshedAt"`
	Fields      map[string][]string `json:"fields"`
}

func (s *Service) ready(ctx context.Context) error {
	if err := access.RequireAdmin(ctx); err != nil {
		return err
	}
	if s.src == nil {
		return domain.Unavailable("Vidispine is not configured")
	}
	return nil
}

// upstream turns a Vidispine error into a domain error. The cause stays in the log: it names
// internal hosts.
func upstream(ctx context.Context, err error, itemID string) error {
	if errors.Is(err, vidispine.ErrNotFound) {
		// A wrong VIDISPINE_URL answers 404 for every item, so the requested URL is logged.
		slog.WarnContext(ctx, "vidispine: not found", "item", itemID, "error", err)
		return fmt.Errorf("%w: Vidispine has no item %s", domain.ErrNotFound, itemID)
	}
	slog.ErrorContext(ctx, "vidispine: request failed", "item", itemID, "error", err)
	return domain.Unavailable("Vidispine did not answer")
}

func metadataOf(m *vidispine.MetadataResult, itemID string) json.RawMessage {
	raw, _ := json.Marshal(Metadata{ItemID: itemID, RefreshedAt: time.Now().UTC().Truncate(time.Second), Fields: m.ItemValues()})
	return raw
}

// Import creates an asset from one Vidispine item: its metadata and, when the item has one, the
// file of its original shape as a master rendition. An item can be imported once.
func (s *Service) Import(ctx context.Context, in ImportInput) (sqlc.Asset, error) {
	if err := s.ready(ctx); err != nil {
		return sqlc.Asset{}, err
	}
	itemID := strings.TrimSpace(in.ItemID)
	if !itemIDPattern.MatchString(itemID) {
		return sqlc.Asset{}, domain.Invalid("itemId must look like VX-123")
	}

	// Everything is read from Vidispine before the transaction starts, so that no database
	// transaction stays open while waiting for another system.
	meta, err := s.src.GetMetadata(ctx, itemID)
	if err != nil {
		return sqlc.Asset{}, upstream(ctx, err, itemID)
	}
	shapes, err := s.src.GetShapes(ctx, itemID)
	if err != nil {
		return sqlc.Asset{}, upstream(ctx, err, itemID)
	}
	orig, err := originalOf(shapes)
	if err != nil {
		return sqlc.Asset{}, err
	}
	var location file.LocationInput
	if orig != nil {
		storage, err := s.src.GetStorageMethods(ctx, orig.storageID)
		if err != nil {
			slog.ErrorContext(ctx, "vidispine: storage request failed", "item", itemID, "storage", orig.storageID, "error", err)
			return sqlc.Asset{}, domain.Unavailable("Vidispine did not answer")
		}
		if location, err = locationOf(orig.storageID, storage); err != nil {
			return sqlc.Asset{}, err
		}
	}
	assetMeta, _ := json.Marshal(map[string]json.RawMessage{MetadataKey: metadataOf(meta, itemID)})

	// The services below run in this transaction, so what they record only happened once it
	// commits.
	caller := ctx
	ctx, release := access.HoldAudit(ctx)
	var a sqlc.Asset
	err = s.tx.InTx(ctx, func(q sqlc.Querier) error {
		if err := q.LockLegacyID(ctx, itemID); err != nil {
			return err
		}
		existing, err := q.GetAssetByLegacyID(ctx, itemID)
		if err == nil {
			return domain.Conflict("%s is already imported as %s", itemID, existing.ID)
		}
		if !errors.Is(domain.FromPG(err), domain.ErrNotFound) {
			return err
		}

		// The other services run on this transaction, so their rules apply and a failure
		// leaves nothing behind.
		tx := db.SameTx(q)
		assets, files := asset.NewService(q, tx), file.NewService(q, tx)
		a, err = assets.Create(ctx, asset.Input{
			TypeID: in.TypeID, Title: titleOf(meta, itemID), Metadata: assetMeta, LegacyIDs: []string{itemID},
		}, in.ProjectID)
		if err != nil || orig == nil {
			return err
		}
		label := versionLabel
		v, err := assets.CreateVersion(ctx, a.ID, asset.VersionInput{Label: &label})
		if err != nil {
			return err
		}
		r, err := assets.CreateRendition(ctx, v.ID, asset.RenditionInput{Kind: sqlc.RenditionKindMaster, Metadata: orig.renditionMetadata()})
		if err != nil {
			return err
		}
		loc, err := q.GetStorageLocationByName(ctx, location.Name)
		if errors.Is(domain.FromPG(err), domain.ErrNotFound) {
			loc, err = files.CreateLocation(ctx, location)
		}
		if err != nil {
			return err
		}
		f, err := files.Register(ctx, orig.registerInput(loc.ID))
		if err != nil {
			return err
		}
		_, err = assets.AttachFile(ctx, r.ID, f.ID, orig.role, 0)
		return err
	})
	release(err == nil)
	return a, access.Audited(caller, err, "vidispine.import", a.ID, "item", itemID)
}

// Refresh replaces the Vidispine metadata of an imported asset with Vidispine's current values.
// Title, description and everything else edited in MediaBrain stay as they are.
func (s *Service) Refresh(ctx context.Context, assetID string) (sqlc.Asset, error) {
	if err := s.ready(ctx); err != nil {
		return sqlc.Asset{}, err
	}
	a, err := s.q.GetAsset(ctx, assetID)
	if err != nil {
		return sqlc.Asset{}, domain.FromPG(err)
	}
	itemID := ItemID(a)
	if itemID == "" {
		return sqlc.Asset{}, domain.Invalid("the asset was not imported from Vidispine")
	}
	meta, err := s.src.GetMetadata(ctx, itemID)
	if err != nil {
		return sqlc.Asset{}, upstream(ctx, err, itemID)
	}
	a, err = s.q.SetAssetMetadataKey(ctx, sqlc.SetAssetMetadataKeyParams{ID: assetID, Key: MetadataKey, Value: metadataOf(meta, itemID)})
	return a, access.Audited(ctx, domain.FromPG(err), "vidispine.refresh", assetID, "item", itemID)
}

// ItemID returns the Vidispine item an asset was imported from, or "".
func ItemID(a sqlc.Asset) string {
	var m map[string]json.RawMessage
	var c Metadata
	if json.Unmarshal(a.Metadata, &m) == nil && json.Unmarshal(m[MetadataKey], &c) == nil && itemIDPattern.MatchString(c.ItemID) {
		return c.ItemID
	}
	// The metadata is editable, the legacy id is how the import marked the asset.
	for _, id := range a.LegacyIds {
		if itemIDPattern.MatchString(id) {
			return id
		}
	}
	return ""
}
