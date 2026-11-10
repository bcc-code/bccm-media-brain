// Package graph contains the GraphQL resolvers. They stay thin: rules and permission checks
// live in the domain services.
package graph

//go:generate go tool gqlgen generate

import (
	"github.com/bcc-media/mediabrain/internal/access"
	"github.com/bcc-media/mediabrain/internal/asset"
	"github.com/bcc-media/mediabrain/internal/file"
	"github.com/bcc-media/mediabrain/internal/project"
	"github.com/bcc-media/mediabrain/internal/subtitle"
)

type Resolver struct {
	AccessSvc    *access.Service
	ProjectsSvc  *project.Service
	FilesSvc     *file.Service
	AssetsSvc    *asset.Service
	SubtitlesSvc *subtitle.Service
}
