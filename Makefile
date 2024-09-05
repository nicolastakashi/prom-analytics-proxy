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

all: fmt vet tests deps build

.PHONY: build