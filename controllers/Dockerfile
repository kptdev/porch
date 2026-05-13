# Copyright 2022-2026 The kpt Authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

ARG ALPINE_VERSION=latest
ARG GOLANG_BOOKWORM_VERSION=latest

FROM golang:${GOLANG_BOOKWORM_VERSION} AS builder

WORKDIR /go/src

# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
COPY go.mod go.sum ./
COPY ./third_party ./third_party

RUN --mount=type=cache,target=/go/pkg/mod \
    echo "Downloading porch modules ..." \
 && go mod download

ENV CGO_ENABLED=0

COPY ./api ./api
COPY ./cmd ./cmd
COPY ./pkg ./pkg
COPY ./internal ./internal
COPY ./controllers ./controllers
COPY ./func ./func

WORKDIR /go/src/controllers/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /porch-controllers -v .

FROM alpine:${ALPINE_VERSION}
RUN addgroup -g 10001 nonroot && \
    adduser -D -G nonroot -u 10001 nonroot

COPY --from=builder --chown=root:root --chmod=555 /porch-controllers /home/nonroot/porch-controllers
USER nonroot:nonroot
ENTRYPOINT ["/home/nonroot/porch-controllers"]
