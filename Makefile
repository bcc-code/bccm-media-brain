SHELL := /bin/bash

# Configuration (DATABASE_URL etc.) is read by the binary itself from the
# environment and .env — make does not need to know about it.
MIGRATIONS := internal/db/migrations
MB         := go run ./cmd/mediabrain

.PHONY: help dev db-up db-down db-reset migrate-up migrate-down migrate-status migrate-new \
        seed seed-list reset-data admin-grant admin-revoke cantemo-import cantemo-refresh generate fmt run build vet test test-e2e cover check hooks clean

help: ## Show targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-16s %s\n",$$1,$$2}'

dev: ## One-command dev env: .env, Postgres, live-reloading server + debug UI
	./scripts/dev.sh

db-up: ## Start local Postgres (docker compose)
	docker compose -f deploy/docker-compose.yml up -d --wait

db-down: ## Stop local Postgres
	docker compose -f deploy/docker-compose.yml down

db-reset: ## Drop all migrations and re-apply them (wipes data)
	$(MB) migrate reset

migrate-up: ## Apply migrations
	$(MB) migrate up

migrate-down: ## Roll back the last migration
	$(MB) migrate down

migrate-status: ## Show migration status
	$(MB) migrate status

migrate-new: ## Create a migration: make migrate-new name=add_foo
	@test -n "$(name)" || (echo "usage: make migrate-new name=..." && exit 1)
	go tool goose -dir $(MIGRATIONS) -s create $(name) sql

seed: ## Run a dev-data scenario: make seed s=catalog args="-reset -seed 42 assets=20"
	@test -n "$(s)" || (echo 'usage: make seed s=<scenario> [args="..."]  (see make seed-list)' && exit 1)
	$(MB) seed $(s) $(args)

seed-list: ## List dev-data scenarios
	$(MB) seed list

reset-data: ## Truncate all application tables
	$(MB) reset-data

admin-grant: ## Make a signed-in user a system administrator: make admin-grant u=me@example.com
	@test -n "$(u)" || (echo "usage: make admin-grant u=<email or user id>" && exit 1)
	$(MB) admin grant $(u)

admin-revoke: ## Remove system administrator rights: make admin-revoke u=me@example.com
	@test -n "$(u)" || (echo "usage: make admin-revoke u=<email or user id>" && exit 1)
	$(MB) admin revoke $(u)

cantemo-import: ## Import one Cantemo item: make cantemo-import id=VX-123 [args="-type 2 -project PJ-..."]
	@test -n "$(id)" || (echo 'usage: make cantemo-import id=<VX-id> [args="-type N -project PJ-..."]' && exit 1)
	$(MB) cantemo import $(args) $(id)

cantemo-refresh: ## Refresh an imported asset's Cantemo metadata: make cantemo-refresh a=AS-...
	@test -n "$(a)" || (echo "usage: make cantemo-refresh a=<asset id>" && exit 1)
	$(MB) cantemo refresh $(a)

fmt: ## gofmt all Go files
	gofmt -w cmd internal pkg e2e

generate: ## Regenerate sqlc + gqlgen code
	go tool sqlc generate
	go tool gqlgen generate

build: ## Build the server binary into bin/
	go build -o bin/mediabrain ./cmd/mediabrain

run: ## Run the server once, without live reload
	$(MB) serve

vet: ## go vet (including e2e-tagged code)
	go vet ./...
	go vet -tags e2e ./...

test: ## Unit tests (no docker)
	go test -race ./...

test-e2e: ## E2E tests against Postgres in docker (testcontainers)
	go test -tags e2e -count=1 ./e2e/...

cover: ## Unit + e2e with coverage gate (>= 90%)
	./scripts/coverage.sh

check: vet cover ## Everything required before committing

hooks: ## Install the git pre-commit hook (runs make check)
	git config core.hooksPath scripts/githooks

clean:
	rm -rf bin coverage tmp
