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
	ReadFile(ctx context.Context, fileID string, limit int64) ([]byte, error)
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

// Import creates an asset from one Vidispine item: its metadata and the files of its shapes (see
// plan). An item can be imported once; Refresh brings an imported one up to date.
func (s *Service) Import(ctx context.Context, in ImportInput) (sqlc.Asset, error) {
	if err := s.ready(ctx); err != nil {
		return sqlc.Asset{}, err
	}
	itemID := strings.TrimSpace(in.ItemID)
	if !itemIDPattern.MatchString(itemID) {
		return sqlc.Asset{}, domain.InvalidField("itemId", "itemId must look like VX-123")
	}
	p, err := s.read(ctx, itemID)
	if err != nil {
		return sqlc.Asset{}, err
	}
	assetMeta, _ := json.Marshal(map[string]json.RawMessage{MetadataKey: p.metadata})

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
			return domain.Exists("%s is already imported as %s", itemID, existing.ID)
		}
		if !errors.Is(domain.FromPG(err), domain.ErrNotFound) {
			return err
		}
		a, err = asset.NewService(q, db.SameTx(q)).Create(ctx, asset.Input{
			TypeID: in.TypeID, Title: titleOf(p.meta, itemID), Metadata: assetMeta, LegacyIDs: []string{itemID},
		}, in.ProjectID)
		if err != nil {
			return err
		}
		return p.apply(ctx, q, a)
	})
	release(err == nil)
	return a, access.Audited(caller, err, "vidispine.import", a.ID, "item", itemID)
}

// Refresh re-imports an imported asset. It replaces the Vidispine metadata with Vidispine's
// current values and adds what the asset is missing: an original that a placeholder got since,
// a preview, a new transcript (as a new revision). Title, description, other metadata and the
// files already there stay as they are: a shape that was replaced in Vidispine is not swapped.
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
	p, err := s.read(ctx, itemID)
	if err != nil {
		return sqlc.Asset{}, err
	}
	caller := ctx
	ctx, release := access.HoldAudit(ctx)
	err = s.tx.InTx(ctx, func(q sqlc.Querier) error {
		if err := q.LockLegacyID(ctx, itemID); err != nil {
			return err
		}
		a, err = q.SetAssetMetadataKey(ctx, sqlc.SetAssetMetadataKeyParams{ID: assetID, Key: MetadataKey, Value: p.metadata})
		if err != nil {
			return domain.FromPG(err)
		}
		return p.apply(ctx, q, a)
	})
	release(err == nil)
	return a, access.Audited(caller, err, "vidispine.refresh", assetID, "item", itemID)
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
