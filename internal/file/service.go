// Package file registers file content (identified by hash), its copies and storage locations.
package file

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/bcc-media/mediabrain/internal/access"
	"github.com/bcc-media/mediabrain/internal/db"
	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/domain"
)

var hashBytes = map[sqlc.HashAlgo]int{
	sqlc.HashAlgoXxh3128: 16,
	sqlc.HashAlgoBlake3:  32,
	sqlc.HashAlgoSha256:  32,
	sqlc.HashAlgoMd5:     16,
	sqlc.HashAlgoSha1:    20,
}

type Service struct {
	q  sqlc.Querier
	tx db.TxRunner
}

func NewService(q sqlc.Querier, tx db.TxRunner) *Service { return &Service{q: q, tx: tx} }

type LocationInput struct {
	Name    string
	Kind    sqlc.LocationKind
	BaseURI string
	Online  bool
}

func (in *LocationInput) validate() error {
	var err error
	if in.Name, err = domain.Required("name", in.Name); err != nil {
		return err
	}
	if in.BaseURI, err = domain.Required("baseUri", in.BaseURI); err != nil {
		return err
	}
	if !in.Kind.Valid() {
		return domain.Invalid("unknown kind")
	}
	return nil
}

func (s *Service) CreateLocation(ctx context.Context, in LocationInput) (sqlc.StorageLocation, error) {
	if err := access.RequireAdmin(ctx); err != nil {
		return sqlc.StorageLocation{}, err
	}
	if err := in.validate(); err != nil {
		return sqlc.StorageLocation{}, err
	}
	l, err := s.q.CreateStorageLocation(ctx, sqlc.CreateStorageLocationParams{
		Name: in.Name, Kind: in.Kind, BaseUri: in.BaseURI, Online: in.Online,
	})
	return l, domain.FromPG(err)
}

func (s *Service) UpdateLocation(ctx context.Context, id string, in LocationInput) (sqlc.StorageLocation, error) {
	if err := access.RequireAdmin(ctx); err != nil {
		return sqlc.StorageLocation{}, err
	}
	if err := in.validate(); err != nil {
		return sqlc.StorageLocation{}, err
	}
	l, err := s.q.UpdateStorageLocation(ctx, sqlc.UpdateStorageLocationParams{
		ID: id, Name: in.Name, Kind: in.Kind, BaseUri: in.BaseURI, Online: in.Online,
	})
	return l, domain.FromPG(err)
}

func (s *Service) DeleteLocation(ctx context.Context, id string) error {
	if err := access.RequireAdmin(ctx); err != nil {
		return err
	}
	return deleted(s.q.DeleteStorageLocation(ctx, id))
}

func deleted(n int64, err error) error {
	if err != nil {
		return domain.FromPGDelete(err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Service) Locations(ctx context.Context) ([]sqlc.StorageLocation, error) {
	if err := access.RequireAdmin(ctx); err != nil {
		return nil, err
	}
	return s.q.ListStorageLocations(ctx)
}

type HashInput struct {
	Algo  sqlc.HashAlgo
	Value string // hex
}

type CopyInput struct {
	LocationID string
	Path       string
}

type RegisterInput struct {
	SizeBytes  int64
	MimeType   *string
	DurationMs *int64
	Codec      *string
	Width      *int32
	Height     *int32
	Probe      json.RawMessage
	Hashes     []HashInput
	Copies     []CopyInput
}

func decodeHash(h HashInput) ([]byte, error) {
	want, ok := hashBytes[h.Algo]
	if !ok {
		return nil, domain.Invalid("unknown hash algorithm")
	}
	raw, err := hex.DecodeString(strings.TrimSpace(h.Value))
	if err != nil || len(raw) != want {
		return nil, domain.Invalid("%s hash must be %d hex characters", h.Algo, want*2)
	}
	return raw, nil
}

func cleanPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" || strings.HasPrefix(p, "/") {
		return "", domain.Invalid("path must be relative to the storage location")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "", domain.Invalid("path must not contain ..")
		}
	}
	return p, nil
}

// Register records a file from metadata supplied by the caller. Content is identified by its
// hashes: when one of them is already known, the existing file is returned and any new hashes
// and copies are added to it.
func (s *Service) Register(ctx context.Context, in RegisterInput) (sqlc.File, error) {
	if err := access.RequireAdmin(ctx); err != nil {
		return sqlc.File{}, err
	}
	switch {
	case in.SizeBytes < 0:
		return sqlc.File{}, domain.Invalid("sizeBytes must not be negative")
	case in.DurationMs != nil && *in.DurationMs < 0:
		return sqlc.File{}, domain.Invalid("durationMs must not be negative")
	case (in.Width != nil && *in.Width <= 0) || (in.Height != nil && *in.Height <= 0):
		return sqlc.File{}, domain.Invalid("width and height must be positive")
	case len(in.Hashes) == 0:
		return sqlc.File{}, domain.Invalid("at least one hash is required")
	}
	probe, err := domain.JSONObject("probe", in.Probe)
	if err != nil {
		return sqlc.File{}, err
	}
	hashes := map[sqlc.HashAlgo][]byte{}
	for _, h := range in.Hashes {
		raw, err := decodeHash(h)
		if err != nil {
			return sqlc.File{}, err
		}
		if _, dup := hashes[h.Algo]; dup {
			return sqlc.File{}, domain.Invalid("hash algorithm %s given twice", h.Algo)
		}
		hashes[h.Algo] = raw
	}
	copies := make([]CopyInput, len(in.Copies))
	for i, c := range in.Copies {
		p, err := cleanPath(c.Path)
		if err != nil {
			return sqlc.File{}, err
		}
		copies[i] = CopyInput{LocationID: c.LocationID, Path: p}
	}

	var f sqlc.File
	err = s.tx.InTx(ctx, func(q sqlc.Querier) error {
		found := false
		for algo, value := range hashes {
			existing, err := q.GetFileByHash(ctx, sqlc.GetFileByHashParams{Algo: algo, Value: value})
			if errors.Is(domain.FromPG(err), domain.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			if found && existing.ID != f.ID {
				return domain.Conflict("the hashes belong to different files")
			}
			f, found = existing, true
		}
		if found {
			if f.SizeBytes != in.SizeBytes {
				return domain.Conflict("a file with this hash has a different size")
			}
			known, err := q.ListFileHashes(ctx, []string{f.ID})
			if err != nil {
				return err
			}
			for _, k := range known {
				if v, ok := hashes[k.Algo]; ok {
					if string(v) != string(k.Value) {
						return domain.Conflict("the file has a different %s hash", k.Algo)
					}
					delete(hashes, k.Algo)
				}
			}
		} else {
			f, err = q.CreateFile(ctx, sqlc.CreateFileParams{
				SizeBytes: in.SizeBytes, MimeType: domain.Blank(in.MimeType), DurationMs: in.DurationMs,
				Codec: domain.Blank(in.Codec), Width: in.Width, Height: in.Height, Probe: probe,
			})
			if err != nil {
				return domain.FromPG(err)
			}
		}
		for algo, value := range hashes {
			if err := q.AddFileHash(ctx, sqlc.AddFileHashParams{FileID: f.ID, Algo: algo, Value: value}); err != nil {
				return domain.FromPG(err)
			}
		}
		for _, c := range copies {
			if _, err := addCopy(ctx, q, f.ID, c); err != nil {
				return err
			}
		}
		return nil
	})
	return f, err
}

func addCopy(ctx context.Context, q sqlc.Querier, fileID string, c CopyInput) (sqlc.FileCopy, error) {
	existing, err := q.GetFileCopyByPath(ctx, sqlc.GetFileCopyByPathParams{LocationID: c.LocationID, Path: c.Path})
	if err == nil {
		if existing.FileID != fileID {
			return sqlc.FileCopy{}, domain.Conflict("%s already holds a different file", c.Path)
		}
		return existing, nil
	}
	if !errors.Is(domain.FromPG(err), domain.ErrNotFound) {
		return sqlc.FileCopy{}, err
	}
	now := time.Now()
	cp, err := q.CreateFileCopy(ctx, sqlc.CreateFileCopyParams{
		FileID: fileID, LocationID: c.LocationID, Path: c.Path, LastSeenAt: &now,
	})
	return cp, domain.FromPG(err)
}

// visible returns the file if the caller is an admin or can view an asset that uses it.
func (s *Service) visible(ctx context.Context, f sqlc.File, err error) (sqlc.File, error) {
	if err != nil {
		return sqlc.File{}, domain.FromPG(err)
	}
	have, err := access.FileLevel(ctx, s.q, f.ID)
	if err != nil {
		return sqlc.File{}, err
	}
	return f, access.Decide(have, access.View)
}

func (s *Service) Get(ctx context.Context, id string) (sqlc.File, error) {
	f, err := s.q.GetFile(ctx, id)
	return s.visible(ctx, f, err)
}

func (s *Service) ByHash(ctx context.Context, h HashInput) (sqlc.File, error) {
	raw, err := decodeHash(h)
	if err != nil {
		return sqlc.File{}, err
	}
	f, err := s.q.GetFileByHash(ctx, sqlc.GetFileByHashParams{Algo: h.Algo, Value: raw})
	return s.visible(ctx, f, err)
}

func (s *Service) List(ctx context.Context, limit, offset int) ([]sqlc.File, error) {
	if err := access.RequireAdmin(ctx); err != nil {
		return nil, err
	}
	l, o, err := domain.Page(limit, offset)
	if err != nil {
		return nil, err
	}
	return s.q.ListFiles(ctx, sqlc.ListFilesParams{Limit: l, Offset: o})
}

// HashesFor, CopiesFor and LocationsByID load children for files the caller was already
// allowed to see; they are the batch side of the GraphQL dataloaders.
func (s *Service) HashesFor(ctx context.Context, fileIDs []string) (map[string][]sqlc.FileHash, error) {
	rows, err := s.q.ListFileHashes(ctx, fileIDs)
	out := map[string][]sqlc.FileHash{}
	for _, r := range rows {
		out[r.FileID] = append(out[r.FileID], r)
	}
	return out, err
}

// CopiesFor lists where files are stored. Files the caller has less than download_original on
// get no copies.
func (s *Service) CopiesFor(ctx context.Context, fileIDs []string) (map[string][]sqlc.FileCopy, error) {
	levels, err := access.FileLevels(ctx, s.q, fileIDs)
	if err != nil {
		return nil, err
	}
	var allowed []string
	for id, l := range levels {
		if access.Allows(l, access.Download) {
			allowed = append(allowed, id)
		}
	}
	out := map[string][]sqlc.FileCopy{}
	if len(allowed) == 0 {
		return out, nil
	}
	rows, err := s.q.ListFileCopies(ctx, allowed)
	for _, r := range rows {
		out[r.FileID] = append(out[r.FileID], r)
	}
	return out, err
}

func (s *Service) LocationsByID(ctx context.Context, ids []string) (map[string]sqlc.StorageLocation, error) {
	rows, err := s.q.GetStorageLocations(ctx, ids)
	out := make(map[string]sqlc.StorageLocation, len(rows))
	for _, r := range rows {
		out[r.ID] = r
	}
	return out, err
}

func (s *Service) ByID(ctx context.Context, ids []string) (map[string]sqlc.File, error) {
	rows, err := s.q.GetFiles(ctx, ids)
	out := make(map[string]sqlc.File, len(rows))
	for _, r := range rows {
		out[r.ID] = r
	}
	return out, err
}

func (s *Service) AddCopy(ctx context.Context, fileID string, c CopyInput) (sqlc.FileCopy, error) {
	if err := access.RequireAdmin(ctx); err != nil {
		return sqlc.FileCopy{}, err
	}
	p, err := cleanPath(c.Path)
	if err != nil {
		return sqlc.FileCopy{}, err
	}
	if _, err := s.q.GetFile(ctx, fileID); err != nil {
		return sqlc.FileCopy{}, domain.FromPG(err)
	}
	return addCopy(ctx, s.q, fileID, CopyInput{LocationID: c.LocationID, Path: p})
}

// SetCopyStatus records the result of checking a copy: present and mismatch mean it was seen,
// and every check counts as a verification.
func (s *Service) SetCopyStatus(ctx context.Context, copyID string, status sqlc.CopyStatus) (sqlc.FileCopy, error) {
	if err := access.RequireAdmin(ctx); err != nil {
		return sqlc.FileCopy{}, err
	}
	if !status.Valid() {
		return sqlc.FileCopy{}, domain.Invalid("unknown status")
	}
	c, err := s.q.GetFileCopy(ctx, copyID)
	if err != nil {
		return sqlc.FileCopy{}, domain.FromPG(err)
	}
	now := time.Now()
	seen := c.LastSeenAt
	if status != sqlc.CopyStatusMissing {
		seen = &now
	}
	c, err = s.q.SetFileCopyStatus(ctx, sqlc.SetFileCopyStatusParams{
		ID: copyID, Status: status, LastSeenAt: seen, LastVerifiedAt: &now,
	})
	return c, domain.FromPG(err)
}

func (s *Service) RemoveCopy(ctx context.Context, copyID string) error {
	if err := access.RequireAdmin(ctx); err != nil {
		return err
	}
	return deleted(s.q.DeleteFileCopy(ctx, copyID))
}

// Delete removes a file that has no copies and is not used by any rendition or timeline.
func (s *Service) Delete(ctx context.Context, id string) error {
	if err := access.RequireAdmin(ctx); err != nil {
		return err
	}
	return deleted(s.q.DeleteFile(ctx, id))
}
