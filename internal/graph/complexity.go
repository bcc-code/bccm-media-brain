package graph

import (
	"github.com/bcc-media/mediabrain/internal/asset"
	"github.com/bcc-media/mediabrain/internal/db/sqlc"
	"github.com/bcc-media/mediabrain/internal/domain"
	"github.com/bcc-media/mediabrain/internal/graph/generated"
	"github.com/bcc-media/mediabrain/internal/project"
	"github.com/bcc-media/mediabrain/internal/subtitle"
)

// ComplexityLimit caps the cost of one operation. A field costs 1; a list costs 1 plus the
// cost of its selection once per item it may return, so the number is roughly how many values
// the answer could hold if every list were full. Lists are rarely full (the default page of
// versions is 50, an asset has a handful), so the limit is generous: one million lets through
// one asset with everything down to its subtitle cues (543 560) and a page of 100 assets with
// versions, renditions and files (860 201), and refuses 500 assets with those three levels
// (4 301 001) or 100 assets with their whole trees (4 983 401) unless the nested lists are
// given a smaller limit. TestComplexityOfQueries has the sums.
const ComplexityLimit = 1_000_000

// How many items a list without a limit argument is taken to return. These are estimates of a
// generous real case, not bounds: such lists are not paged because they stay small.
const (
	assumedRenditions = 10  // per version: master, encodes, dubs, subtitles
	assumedFiles      = 5   // per rendition
	assumedTracks     = 2   // per rendition, latest revisions only
	assumedHistory    = 10  // per rendition or asset, with every revision
	assumedHashes     = 3   // per file
	assumedCopies     = 3   // per file
	assumedLinks      = 3   // projects per asset
	assumedReference  = 20  // asset types, storage locations, the tokens of a service account
	assumedDirectory  = 100 // groups, service accounts, the grants on one object
	assumedLanguages  = 200

	// An export reads the whole track, like a default page of cues.
	exportCost = 500

	// maxCost is where a cost stops growing, so nested lists cannot overflow into a small number.
	maxCost = 1 << 40
)

// listCost is the cost of a list of up to items entries whose selection costs child.
func listCost(child, items int) int {
	if child < 1 {
		child = 1
	}
	if items >= maxCost/child {
		return maxCost
	}
	return 1 + items*child
}

// pageSize is how many items a limit argument asks for: def when it is absent or not positive,
// and at most max, because the service refuses anything larger.
func pageSize(limit *int, def, max int) int {
	switch {
	case limit == nil || *limit <= 0:
		return def
	case *limit > max:
		return max
	}
	return *limit
}

// paged is the cost of a connection: its selection once per item the page may hold. The
// selection of a connection is its nodes, whose cost is one node's (see nodesCost).
func paged(def, max int) func(int, *int, *string) int {
	return func(child int, first *int, _ *string) int { return listCost(child, pageSize(first, def, max)) }
}

// connectionPart is the cost of a connection's nodes and of its pageInfo: what is selected
// inside, and nothing for the field itself. The page size is an argument of the field that
// returns the connection, so that field counts the list, once per item, and nodes must cost
// what one node costs. pageInfo is counted per item too, which overstates it by at most two
// per item: gqlgen ignores a cost below the selection's, so it cannot be made free.
func connectionPart(child int) int { return child }

func assumed(items int) func(int) int {
	return func(child int) int { return listCost(child, items) }
}

func trackCost(child int, history bool) int {
	if history {
		return listCost(child, assumedHistory)
	}
	return listCost(child, assumedTracks)
}

// complexity gives every list field its cost. A list field added to the schema needs a line
// here; without one it costs like a single object.
func complexity() generated.ComplexityRoot {
	var c generated.ComplexityRoot
	page := paged(domain.DefaultLimit, domain.MaxLimit)

	c.Query.Assets = func(child int, _ *asset.Filter, first *int, after *string) int { return page(child, first, after) }
	c.Query.Files = page
	c.Query.Programs = page
	c.Query.Users = page
	c.Query.Projects = func(child int, _ *string, _ *sqlc.ProjectStatus, first *int, after *string) int {
		return page(child, first, after)
	}
	c.Query.AssetTypes = assumed(assumedReference)
	c.Query.StorageLocations = assumed(assumedReference)
	c.Query.Languages = func(child int, _ bool) int { return listCost(child, assumedLanguages) }
	c.Query.UserGroups = assumed(assumedDirectory)
	c.Query.ServiceAccounts = assumed(assumedDirectory)
	c.Query.ServiceTokens = func(child int, _ string) int { return listCost(child, assumedReference) }
	c.Query.Grants = func(child int, _ sqlc.GrantScope, _ string) int { return listCost(child, assumedDirectory) }
	c.Query.SubtitleTracks = func(child int, _ string, history bool) int { return trackCost(child, history) }

	c.Asset.Versions = page
	c.Asset.Projects = assumed(assumedLinks)
	c.AssetVersion.Renditions = assumed(assumedRenditions)
	c.Rendition.Files = assumed(assumedFiles)
	c.Rendition.SubtitleTracks = trackCost
	c.File.Hashes = assumed(assumedHashes)
	c.File.Copies = assumed(assumedCopies)
	c.Program.Projects = paged(project.ProjectsPerProgram, domain.MaxLimit)
	c.SubtitleTrack.Cues = paged(subtitle.DefaultCuePage, subtitle.MaxCuePage)
	c.SubtitleTrack.Export = func(int, subtitle.Format) int { return exportCost }

	c.AssetConnection.Nodes, c.AssetConnection.PageInfo = connectionPart, connectionPart
	c.AssetVersionConnection.Nodes, c.AssetVersionConnection.PageInfo = connectionPart, connectionPart
	c.FileConnection.Nodes, c.FileConnection.PageInfo = connectionPart, connectionPart
	c.ProgramConnection.Nodes, c.ProgramConnection.PageInfo = connectionPart, connectionPart
	c.ProjectConnection.Nodes, c.ProjectConnection.PageInfo = connectionPart, connectionPart
	c.SubtitleCueConnection.Nodes, c.SubtitleCueConnection.PageInfo = connectionPart, connectionPart
	c.UserConnection.Nodes, c.UserConnection.PageInfo = connectionPart, connectionPart
	return c
}
