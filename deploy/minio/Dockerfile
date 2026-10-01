# Copyright 2026 The Gravix Authors
# SPDX-License-Identifier: Apache-2.0
#
# MinIO stopped publishing its community images: minio/minio on Docker Hub
# returns 404, and quay.io/minio/minio answers "unauthorized" (F-057). The full
# stack builds the same pinned releases from source instead, which takes about
# two minutes on a 4-core machine. MinIO is AGPL-3.0; this builds it for a local
# stack and distributes nothing.
FROM golang:1.24-alpine AS build
ARG MINIO_RELEASE=RELEASE.2024-03-15T01-07-19Z
ARG MC_RELEASE=RELEASE.2024-03-13T23-51-57Z
ENV CGO_ENABLED=0
RUN go install "github.com/minio/minio@${MINIO_RELEASE}" \
 && go install "github.com/minio/mc@${MC_RELEASE}"

FROM alpine:3.21
# hadolint ignore=DL3018
RUN apk add --no-cache ca-certificates
COPY --from=build /go/bin/minio /go/bin/mc /usr/bin/
# The alias the compose health check names. Readiness needs no credentials.
ENV MC_HOST_local=http://localhost:9000
EXPOSE 9000 9001
ENTRYPOINT ["/usr/bin/minio"]
