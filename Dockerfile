# syntax=docker/dockerfile:1.7
#
# Multi-stage build for eob-mcp.
#
# Final image is gcr.io/distroless/static-debian12:nonroot:
#   - no shell, no package manager, no libc
#   - nonroot user (UID/GID 65532)
#   - CA certificates included
# Resulting image is ~15-20 MB.

ARG GO_VERSION=1.24
ARG DISTROLESS_TAG=nonroot

# ---- Stage 1: build ---------------------------------------------------------
FROM golang:${GO_VERSION}-alpine AS build

WORKDIR /src

# Cache module downloads separately from the source tree.
COPY go.mod go.sum* ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown

# CGO_ENABLED=0 produces a fully static binary with no libc dependency.
# -trimpath strips local filesystem paths from the binary.
# -ldflags strips debug info and embeds version metadata.
RUN CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -ldflags="-s -w -buildid= \
            -X main.version=${VERSION} \
            -X main.commit=${COMMIT} \
            -X main.date=${DATE}" \
        -o /out/eob-mcp \
        ./cmd/eob-mcp

# ---- Stage 2: runtime -------------------------------------------------------
FROM gcr.io/distroless/static-debian12:${DISTROLESS_TAG}

# Distroless 'nonroot' image already sets USER 65532:65532, runs as nonroot,
# and provides a minimal /etc/passwd, /etc/group, and CA bundle.

COPY --from=build /out/eob-mcp /eob-mcp

# 8443: MCP HTTP+SSE (TLS-terminated by Service / sidecar)
EXPOSE 8443

ENTRYPOINT ["/eob-mcp"]
