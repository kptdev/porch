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
ARG GOLANG_ALPINE_VERSION=latest
ARG PORCH_GHCR_PREFIX_URL=ghcr.io/kptdev/krm-functions-catalog
ARG DOCKERHUB_MIRROR=docker.io

FROM ${PORCH_GHCR_PREFIX_URL}/apply-setters:v0.3.0 AS apply-setters
FROM ${PORCH_GHCR_PREFIX_URL}/ensure-name-substring:v0.3.0 AS ensure-name-substring
FROM ${PORCH_GHCR_PREFIX_URL}/search-replace:v0.2.4 AS search-replace
FROM ${PORCH_GHCR_PREFIX_URL}/set-annotations:v0.1.8 AS set-annotations
FROM ${PORCH_GHCR_PREFIX_URL}/set-image:v0.2.3 AS set-image
FROM ${PORCH_GHCR_PREFIX_URL}/set-labels:v0.2.5 AS set-labels
FROM ${PORCH_GHCR_PREFIX_URL}/set-namespace:v0.4.6 AS set-namespace
FROM ${PORCH_GHCR_PREFIX_URL}/starlark:v0.5.6 AS starlark
FROM ${PORCH_GHCR_PREFIX_URL}/upsert-resource:v0.2.4 AS upsert-resource


FROM ${DOCKERHUB_MIRROR}/golang:${GOLANG_ALPINE_VERSION} AS builder
WORKDIR /go/src

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go install github.com/grpc-ecosystem/grpc-health-probe@v0.4.52 && \
    cp $GOPATH/bin/grpc-health-probe /grpc-health-probe

COPY go.mod go.sum ./
COPY ./third_party ./third_party
COPY ./api ./api
COPY ./cmd ./cmd
COPY ./pkg ./pkg
COPY ./internal ./internal
COPY ./controllers ./controllers
COPY ./func ./func

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd func ; go build -trimpath -ldflags="-s -w" -v -o /server ./server

FROM ${DOCKERHUB_MIRROR}/alpine:${ALPINE_VERSION}
RUN addgroup -g 10001 nonroot && \
    adduser -D -G nonroot -u 10001 nonroot

COPY --from=apply-setters          --chown=root:root --chmod=555 /usr/local/bin/function /home/nonroot/functions/apply-setters
COPY --from=ensure-name-substring  --chown=root:root --chmod=555 /usr/local/bin/function /home/nonroot/functions/ensure-name-substring
COPY --from=search-replace         --chown=root:root --chmod=555 /usr/local/bin/function /home/nonroot/functions/search-replace
COPY --from=set-annotations        --chown=root:root --chmod=555 /usr/local/bin/function /home/nonroot/functions/set-annotations
COPY --from=set-image              --chown=root:root --chmod=555 /usr/local/bin/function /home/nonroot/functions/set-image
COPY --from=set-labels             --chown=root:root --chmod=555 /usr/local/bin/function /home/nonroot/functions/set-labels
COPY --from=set-namespace          --chown=root:root --chmod=555 /usr/local/bin/function /home/nonroot/functions/set-namespace
COPY --from=starlark               --chown=root:root --chmod=555 /usr/local/bin/function /home/nonroot/functions/starlark
COPY --from=upsert-resource        --chown=root:root --chmod=555 /usr/local/bin/function /home/nonroot/functions/upsert-resource

COPY --from=builder                --chown=root:root --chmod=555 /server /grpc-health-probe /home/nonroot/

USER nonroot:nonroot
EXPOSE 9445/tcp
ENTRYPOINT [ "/home/nonroot/server", "--functions=/home/nonroot/functions" ]
