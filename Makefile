SHELL := /bin/bash
.DEFAULT_GOAL := help

DATABASE_URL ?= postgres://ping:ping@localhost:5432/ping?sslmode=disable
MIGRATIONS_DIR := backend/db/migrations

# Tool versions — single-sourced so local `make tools` and CI can't drift.
GOLANGCI_LINT_VERSION := v2.12.2
SQLC_VERSION := v1.31.1
MIGRATE_VERSION := v4.19.1

.PHONY: help dev docker-up docker-down migrate-up migrate-down sqlc hooks tools \
        verify verify-backend verify-frontend verify-generated test-integration build-e2e

help:                                                            ## list targets
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | sed -E 's/:.*## /|/' | sort | awk -F'|' '{printf "%-20s %s\n", $$1, $$2}'

dev:                                                              ## run API + workers with live reload
	cd backend && air -c .air.toml

docker-up:                                                        ## start Postgres + Redis
	docker compose up -d

docker-down:                                                      ## stop Postgres + Redis
	docker compose down

migrate-up:                                                       ## apply all pending migrations
	@if [ -d $(MIGRATIONS_DIR) ] && [ -n "$$(ls -A $(MIGRATIONS_DIR) 2>/dev/null)" ]; then \
		migrate -path $(MIGRATIONS_DIR) -database "$(DATABASE_URL)" up; \
	else \
		echo "no migrations yet ($(MIGRATIONS_DIR)) — nothing to do"; \
	fi

migrate-down:                                                     ## roll back one migration
	@if [ -d $(MIGRATIONS_DIR) ] && [ -n "$$(ls -A $(MIGRATIONS_DIR) 2>/dev/null)" ]; then \
		migrate -path $(MIGRATIONS_DIR) -database "$(DATABASE_URL)" down 1; \
	else \
		echo "no migrations yet ($(MIGRATIONS_DIR)) — nothing to do"; \
	fi

sqlc:                                                              ## regenerate backend/db from queries
	cd backend && sqlc generate

hooks:                                                            ## install git hooks (lefthook)
	lefthook install

tools:                                                            ## install pinned golangci-lint, sqlc, migrate
	@echo "installing golangci-lint $(GOLANGCI_LINT_VERSION)"
	@curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/$(GOLANGCI_LINT_VERSION)/install.sh \
	  | sh -s -- -b $$(go env GOPATH)/bin $(GOLANGCI_LINT_VERSION)
	@echo "installing sqlc $(SQLC_VERSION)"
	@os=$$(uname -s | tr '[:upper:]' '[:lower:]'); \
	arch=$$(uname -m); [ "$$arch" = "x86_64" ] && arch=amd64; [ "$$arch" = "aarch64" ] && arch=arm64; \
	curl -sSfL "https://github.com/sqlc-dev/sqlc/releases/download/$(SQLC_VERSION)/sqlc_$(SQLC_VERSION:v%=%)_$${os}_$${arch}.tar.gz" \
	  | tar -xz -C $$(go env GOPATH)/bin sqlc
	@echo "installing migrate $(MIGRATE_VERSION)"
	@os=$$(uname -s | tr '[:upper:]' '[:lower:]'); \
	arch=$$(uname -m); [ "$$arch" = "x86_64" ] && arch=amd64; [ "$$arch" = "aarch64" ] && arch=arm64; \
	curl -sSfL "https://github.com/golang-migrate/migrate/releases/download/$(MIGRATE_VERSION)/migrate.$${os}-$${arch}.tar.gz" \
	  | tar -xz -C $$(go env GOPATH)/bin migrate

verify: verify-backend verify-frontend verify-generated          ## the full local gate ~= CI

verify-backend:                                                  ## fmt, vet, lint, race tests, tidy check
	cd backend && gofmt -l . | (! grep .) && go vet ./... \
	  && golangci-lint run ./... && go test -race ./... \
	  && go mod tidy && git diff --exit-code -- go.mod $$( [ -f go.sum ] && echo go.sum )

verify-frontend:                                                 ## type-check, lint, unit tests
	cd frontend && npm run type-check && npm run lint && npm run test

verify-generated:                                                ## sqlc output committed and drift-free
	@if [ -d backend/db/queries ] && [ -n "$$(ls -A backend/db/queries 2>/dev/null)" ]; then \
		$(MAKE) sqlc && git diff --exit-code backend/db; \
	else \
		echo "no sqlc queries yet (backend/db/queries) — nothing to do"; \
	fi

test-integration:                                                ## needs docker-up
	cd backend && go test -race -tags integration ./...

build-e2e:                                                       ## build the API with the test-only time-warp endpoint (PING-022)
	cd backend && go build -tags e2e -o tmp/ping-api-e2e ./cmd/ping
