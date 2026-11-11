SHELL := /bin/bash

# Configuration (DATABASE_URL etc.) is read by the binary itself from the
# environment and .env — make does not need to know about it.
MIGRATIONS := internal/db/migrations
MB         := go run ./cmd/mediabrain

.PHONY: help dev db-up db-down db-reset migrate-up migrate-down migrate-status migrate-new \
        seed seed-list reset-data admin-grant admin-revoke service-create service-token service-revoke service-list vidispine-import vidispine-refresh generate fmt run build docker-build prod-config vet test test-e2e cover check hooks clean

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

service-create: ## Add a service account (machine client): make service-create n=ingest [args="-admin"]
	@test -n "$(n)" || (echo 'usage: make service-create n=<name> [args="-admin"]' && exit 1)
	$(MB) service create $(args) $(n)

service-token: ## Create a token for a service account: make service-token n=ingest [args="-name prod -expires 90d"]
	@test -n "$(n)" || (echo 'usage: make service-token n=<name> [args="-name label -expires 90d"]' && exit 1)
	$(MB) service token $(args) $(n)

service-revoke: ## Revoke a service token: make service-revoke t=TK-...
	@test -n "$(t)" || (echo "usage: make service-revoke t=<token id>" && exit 1)
	$(MB) service revoke $(t)

service-list: ## List service accounts and their tokens
	$(MB) service list

vidispine-import: ## Import one Vidispine item: make vidispine-import id=VX-123 [args="-type 2 -project PJ-..."]
	@test -n "$(id)" || (echo 'usage: make vidispine-import id=<VX-id> [args="-type N -project PJ-..."]' && exit 1)
	$(MB) vidispine import $(args) $(id)

vidispine-refresh: ## Refresh an imported asset's Vidispine metadata: make vidispine-refresh a=AS-...
	@test -n "$(a)" || (echo "usage: make vidispine-refresh a=<asset id>" && exit 1)
	$(MB) vidispine refresh $(a)

fmt: ## gofmt all Go files
	gofmt -w cmd internal pkg e2e

generate: ## Regenerate sqlc + gqlgen code
	go tool sqlc generate
	go tool gqlgen generate

build: ## Build the server binary into bin/
	go build -o bin/mediabrain ./cmd/mediabrain

run: ## Run the server once, without live reload
	$(MB) serve

docker-build: ## Build the mediabrain image (tag=latest)
	docker build -t mediabrain:$(or $(tag),latest) .

prod-config: ## Validate and print the production compose file (needs deploy/.env.prod)
	docker compose -f deploy/docker-compose.prod.yml --env-file deploy/.env.prod config

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
