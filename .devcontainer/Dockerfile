# Development image for Gravix.
#
# It carries the toolchain and nothing else — no Gravix services. The lean
# bootstrap stack runs from docker-compose.bootstrap.yml when you want one; the
# full stack wants about 7.8 GB of RAM and is not something to ask of somebody
# looking at this project for the first time.
FROM golang:1.25-bookworm

ARG DUCKDB_VERSION=1.1.3
ARG PROTOC_VERSION=29.3
ARG PROTOC_GEN_GO_VERSION=v1.36.11

# jq is needed by scripts/gfi_audit.sh; bc by scripts/test_fast.sh; unzip to
# unpack the two release archives below.
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      bc ca-certificates curl git jq make unzip \
 && rm -rf /var/lib/apt/lists/*

# DuckDB CLI — the bare-Parquet and exit-path tests shell out to it to prove
# Gravix's files are readable with no Gravix component installed.
RUN curl -fsSL -o /tmp/duckdb.zip \
      "https://github.com/duckdb/duckdb/releases/download/v${DUCKDB_VERSION}/duckdb_cli-linux-amd64.zip" \
 && unzip -q /tmp/duckdb.zip -d /usr/local/bin \
 && chmod +x /usr/local/bin/duckdb \
 && rm /tmp/duckdb.zip \
 && duckdb --version

# protoc, needed only if you change proto/. Pinned, because a generated file
# that differs by toolchain version is a diff nobody can review.
RUN curl -fsSL -o /tmp/protoc.zip \
      "https://github.com/protocolbuffers/protobuf/releases/download/v${PROTOC_VERSION}/protoc-${PROTOC_VERSION}-linux-x86_64.zip" \
 && unzip -q /tmp/protoc.zip -d /usr/local \
 && rm /tmp/protoc.zip \
 && protoc --version
RUN go install "google.golang.org/protobuf/cmd/protoc-gen-go@${PROTOC_GEN_GO_VERSION}"

# The two linters CI runs, so `make lint` gives the same answer here as there.
RUN go install honnef.co/go/tools/cmd/staticcheck@latest \
 && go install golang.org/x/vuln/cmd/govulncheck@latest

ENV PATH="/go/bin:${PATH}"
WORKDIR /workspaces/gravix-dashboards
