SHELL := /bin/bash

# Configuration (DATABASE_URL etc.) is read by the binary itself from the
# environment and .env — make does not need to know about it.
MIGRATIONS := internal/db/migrations
MB         := go run ./cmd/mediabrain

.PHONY: help dev db-up db-down db-reset migrate-up migrate-down migrate-status migrate-new \
        seed seed-list reset-data generate run build vet test test-e2e cover check hooks clean

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

seed: ## Run a dev-data scenario: make seed s=dummies args="-reset count=100"
	@test -n "$(s)" || (echo 'usage: make seed s=<scenario> [args="..."]  (see make seed-list)' && exit 1)
	$(MB) seed $(s) $(args)

seed-list: ## List dev-data scenarios
	$(MB) seed list

reset-data: ## Truncate all application tables
	$(MB) reset-data

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
