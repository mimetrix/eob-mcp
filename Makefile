SHELL := /bin/bash

BIN              := eob-mcp
CMD              := ./cmd/eob-mcp
VERSION          ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT           ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE             ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS          := -s -w -buildid= \
                    -X main.version=$(VERSION) \
                    -X main.commit=$(COMMIT) \
                    -X main.date=$(DATE)
GOFLAGS          := -trimpath -ldflags="$(LDFLAGS)"

IMAGE_REPO       ?= ghcr.io/mimetrix/eob-mcp
IMAGE_TAG        ?= $(VERSION)
IMAGE            := $(IMAGE_REPO):$(IMAGE_TAG)

.DEFAULT_GOAL := build

.PHONY: build
build:
	CGO_ENABLED=0 go build $(GOFLAGS) -o bin/$(BIN) $(CMD)

.PHONY: build-static
build-static:
	CGO_ENABLED=0 GOOS=linux go build $(GOFLAGS) -o bin/$(BIN) $(CMD)

.PHONY: test
test:
	go test -race -count=1 -timeout=120s ./...

.PHONY: test-cover
test-cover:
	go test -race -count=1 -timeout=120s -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

.PHONY: fuzz
fuzz:
	@echo "running fuzz tests (60s each)"
	@for pkg in $$(go list ./... | xargs -I{} sh -c 'grep -lR "^func Fuzz" {} 2>/dev/null | xargs -r dirname | sort -u'); do \
	    go test -run=^$$ -fuzz=Fuzz -fuzztime=60s $$pkg || exit 1; \
	done

.PHONY: vet
vet:
	go vet ./...

.PHONY: lint
lint: vet
	@command -v golangci-lint >/dev/null 2>&1 || { echo "golangci-lint not installed; see https://golangci-lint.run/usage/install/"; exit 1; }
	golangci-lint run ./...

.PHONY: vuln
vuln:
	@command -v govulncheck >/dev/null 2>&1 || go install golang.org/x/vuln/cmd/govulncheck@latest
	govulncheck ./...

.PHONY: sec
sec:
	@command -v gosec >/dev/null 2>&1 || go install github.com/securego/gosec/v2/cmd/gosec@latest
	gosec -quiet ./...

.PHONY: check
check: vet lint test vuln sec

.PHONY: image
image:
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE) -t $(IMAGE) .

.PHONY: image-scan
image-scan: image
	@command -v trivy >/dev/null 2>&1 || { echo "trivy not installed"; exit 1; }
	trivy image --severity HIGH,CRITICAL --exit-code 1 --no-progress $(IMAGE)

.PHONY: push
push:
	docker push $(IMAGE)

.PHONY: clean
clean:
	rm -rf bin/ coverage.out

.PHONY: tidy
tidy:
	go mod tidy

.PHONY: help
help:
	@echo "Targets:"
	@echo "  build         Build the eob-mcp binary (CGO_ENABLED=0)"
	@echo "  test          Run unit tests with race detector"
	@echo "  test-cover    Run tests and emit coverage report"
	@echo "  fuzz          Run all fuzz tests for 60s each"
	@echo "  vet           Run go vet"
	@echo "  lint          Run golangci-lint (assumes installed)"
	@echo "  vuln          Run govulncheck"
	@echo "  sec           Run gosec"
	@echo "  check         vet + lint + test + vuln + sec"
	@echo "  image         Build the container image"
	@echo "  image-scan    Build and scan the image with trivy"
	@echo "  push          docker push \$$(IMAGE)"
	@echo "  clean         Remove build artifacts"
	@echo "  tidy          go mod tidy"
