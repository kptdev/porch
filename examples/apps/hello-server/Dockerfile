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

FROM golang:1.25.7-bookworm AS builder

WORKDIR /src
COPY go.mod go.sum ./

WORKDIR /src
COPY *.go .

RUN CGO_ENABLED=0 go build -o /hello-server -v .

FROM alpine:${ALPINE_VERSION}
RUN addgroup -g 10001 nonroot && \
    adduser -D -G nonroot -u 10001 nonroot

COPY --from=builder --chown=root:root --chmod=555 /hello-server /home/nonroot/hello-server
USER nonroot:nonroot
ENTRYPOINT ["/home/nonroot/hello-server"]
