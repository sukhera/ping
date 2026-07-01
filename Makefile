SHELL := /bin/bash
.DEFAULT_GOAL := help

DATABASE_URL ?= postgres://ping:ping@localhost:5432/ping?sslmode=disable
MIGRATIONS_DIR := backend/db/migrations

.PHONY: help dev docker-up docker-down migrate-up migrate-down sqlc hooks \
        verify verify-backend verify-frontend verify-generated test-integration

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
