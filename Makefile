TOOLS_BIN_DIR ?= $(shell pwd)/tmp/bin

export PATH := $(TOOLS_BIN_DIR):$(PATH)

GOLANGCILINTER_BINARY=$(TOOLS_BIN_DIR)/golangci-lint
MDOX_BINARY=$(TOOLS_BIN_DIR)/mdox
MDOX_VALIDATE_CONFIG?=.mdox.validate.yaml

TOOLING=$(MDOX_BINARY) $(GOLANGCILINTER_BINARY)

MD_FILES_TO_FORMAT=$(shell ls *.md)

REVISION ?= $(shell git rev-parse HEAD)
BRANCH ?= $(shell git rev-parse --abbrev-ref HEAD)

RELEASE_VERSION ?=$(shell git describe --tags --abbrev=0 2>/dev/null || echo $(REVISION))
BINARY_FOLDER=bin
BINARY_NAME=prom-analytics-proxy
ARTIFACT_NAME=coralogix/$(BINARY_NAME)
GOCMD=go
GOMAIN=main.go
GOBUILD=$(GOCMD) build
GOOS?=$(shell go env GOOS)
ENVVARS=GOOS=$(GOOS) CGO_ENABLED=0

LDFLAGS=-w -extldflags "-static" \
		-X github.com/prometheus/common/version.Version=$(RELEASE_VERSION) \
		-X github.com/prometheus/common/version.Revision=$(REVISION) \
		-X github.com/prometheus/common/version.Branch=$(BRANCH) \
		-X github.com/prometheus/common/version.BuildUser=$(shell whoami) \
		-X "github.com/prometheus/common/version.BuildDate=$(shell date -u)"

.PHONY: docker-build
docker-build:
	@DOCKER_BUILDKIT=1 docker build -t ${ARTIFACT_NAME}:${RELEASE_VERSION} -f Dockerfile --progress=plain .

.PHONY: build
build:
	$(ENVVARS) $(GOCMD) build -ldflags '$(LDFLAGS)' -o $(BINARY_FOLDER)/$(BINARY_NAME) -v $(GOMAIN)

.PHONY: deps
deps:
	$(ENVVARS) $(GOCMD) mod download

.PHONY: fmt
fmt:
	$(ENVVARS) $(GOCMD) fmt -x ./...

.PHONY: vet
vet:
	$(ENVVARS) $(GOCMD) vet ./...

.PHONY: tests
tests:
	$(ENVVARS) $(GOCMD) test ./...

.PHONY: check-golang
check-golang: $(GOLANGCILINTER_BINARY)
	$(GOLANGCILINTER_BINARY) run

.PHONY: fix-golang
fix-golang: $(GOLANGCILINTER_BINARY)
	$(GOLANGCILINTER_BINARY) run --fix

.PHONY: docs
docs: $(MDOX_BINARY)
	@echo ">> formatting and local/remote link check"
	$(MDOX_BINARY) fmt --soft-wraps -l --links.validate.config-file=$(MDOX_VALIDATE_CONFIG) $(MD_FILES_TO_FORMAT)

.PHONY: check-docs
check-docs: $(MDOX_BINARY)
	@echo ">> checking formatting and local/remote links"
	$(MDOX_BINARY) fmt --soft-wraps --check -l --links.validate.config-file=$(MDOX_VALIDATE_CONFIG) $(MD_FILES_TO_FORMAT)

all: fmt vet deps build

.PHONY: tidy
tidy:
	go mod tidy -v
	cd scripts && go mod tidy -v -modfile=go.mod -compat=1.18

.PHONY: migrate.create.pg migrate.create.sqlite migrate.up.pg migrate.up.sqlite

# Create a new timestamped migration for PostgreSQL
migrate.create.pg:
	@[ -n "$$name" ] || (echo "Usage: make migrate.create.pg name=add_users"; exit 1)
	@go run github.com/pressly/goose/v3/cmd/goose@latest -dir internal/db/migrations/postgresql create $$name sql

# Create a new timestamped migration for SQLite
migrate.create.sqlite:
	@[ -n "$$name" ] || (echo "Usage: make migrate.create.sqlite name=add_users"; exit 1)
	@go run github.com/pressly/goose/v3/cmd/goose@latest -dir internal/db/migrations/sqlite create $$name sql

# Run migrations up for Postgres using DSN env POSTGRES_DSN
migrate.up.pg:
	@[ -n "$$POSTGRES_DSN" ] || (echo "Set POSTGRES_DSN, e.g. 'postgres://user:pass@localhost:5432/db?sslmode=disable'"; exit 1)
	@go run github.com/pressly/goose/v3/cmd/goose@latest -dir internal/db/migrations/postgresql postgres "$$POSTGRES_DSN" up

# Run migrations up for SQLite using DB path env SQLITE_DB
migrate.up.sqlite:
	@[ -n "$$SQLITE_DB" ] || (echo "Set SQLITE_DB, e.g. './prom-analytics-proxy.db'"; exit 1)
	@go run github.com/pressly/goose/v3/cmd/goose@latest -dir internal/db/migrations/sqlite sqlite3 "$$SQLITE_DB" up

.PHONY: uibuild
uibuild:
	cd ui && npm run build

.PHONY: uidependencies
uidependencies:
	cd ui && npm install --legacy-peer-deps

.PHONY: build

$(TOOLS_BIN_DIR):
	mkdir -p $(TOOLS_BIN_DIR)

$(TOOLING): $(TOOLS_BIN_DIR)
	@echo Installing tools from scripts/tools.go
	@cat scripts/tools.go | grep _ | awk -F'"' '{print $$2}' | GOBIN=$(TOOLS_BIN_DIR) xargs -tI % go install -mod=readonly -modfile=scripts/go.mod %