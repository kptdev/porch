# SPDX-license-identifier: CC-BY-4.0
# Copyright The kpt authors

SCRIPTS_DIR := $(CURDIR)/../scripts

DOCKER_CMD ?= $(shell which docker 2> /dev/null || which podman 2> /dev/null || echo docker)

ifneq (,$(findstring docker,$(DOCKER_CMD)))
	DOCKER_SUDO = sudo -E
endif

.PHONY: lint
lint:
	$(DOCKER_SUDO) $(DOCKER_CMD) run --rm -v $$(pwd):/tmp/lint \
	-e RUN_LOCAL=true \
	-e LINTER_RULES_PATH=/ \
	-e VALIDATE_NATURAL_LANGUAGE=true \
	docker.io/github/super-linter

.PHONY: generate-api-docs-markdown
generate-api-docs-markdown:
	$(SCRIPTS_DIR)/generate-api-reference-md.sh

# Build the full versioned docs site (all versions from docs/versions.json),
# the same build Netlify runs. Output goes to docs/public by default; override
# with OUTPUT_DIR=<dir>.
#
# The build is run from the canonically resolved repo path (realpath) so it
# works when the checkout is reached through a symlink. Node's filesystem
# permission model (Node >= 23) otherwise denies PostCSS from following the
# symlink during CSS processing, which breaks the Hugo build.
OUTPUT_DIR ?= $(CURDIR)/public

.PHONY: build-versioned
build-versioned:
	@command -v hugo >/dev/null 2>&1 || { echo "Error: hugo not found. See https://gohugo.io/installation/"; exit 1; }
	@command -v npm >/dev/null 2>&1 || { echo "Error: npm not found. See https://nodejs.org/"; exit 1; }
	@real_root="$$(realpath $(CURDIR)/..)"; \
	real_out="$$(realpath -m $(OUTPUT_DIR))"; \
	"$$real_root/scripts/codegen/build-versioned-docs.sh" "$$real_out"

# Build the full versioned site and serve it locally. This is the one command
# to preview the docs exactly as deployed, including the versioned subsites
# (/, /v1.6/, /v1.5/). A plain static file server is used because `hugo server`
# only renders the current working tree and would omit the tagged versions.
# Override the port with SERVE_PORT=<port>.
SERVE_PORT ?= 1313

.PHONY: serve
serve: build-versioned
	@command -v python3 >/dev/null 2>&1 || { echo "Error: python3 not found."; exit 1; }
	@out="$$(realpath -m $(OUTPUT_DIR))"; \
	echo "==> Serving $$out at http://localhost:$(SERVE_PORT)/ (Ctrl+C to stop)"; \
	echo "    latest -> /   v1.6 -> /v1.6/   v1.5 -> /v1.5/"; \
	python3 -m http.server $(SERVE_PORT) --directory "$$out"

.PHONY: serve-dev
serve-dev:
	@command -v hugo >/dev/null 2>&1 || { echo "Error: hugo not found. See https://gohugo.io/installation/"; exit 1; }
	@command -v npm >/dev/null 2>&1 || { echo "Error: npm not found. See https://nodejs.org/"; exit 1; }
	@echo "==> Building Hugo modules and serving at http://localhost:$(SERVE_PORT)/ (live reload enabled)"; \
	npm install && \
	hugo server --port $(SERVE_PORT) --disableFastRender

.PHONY: check-links-external
check-links-external:
	@command -v hugo >/dev/null 2>&1 || { echo "Error: hugo not found. See https://gohugo.io/installation/"; exit 1; }
	@command -v npm >/dev/null 2>&1 || { echo "Error: npm not found. See https://nodejs.org/"; exit 1; }
	@command -v lychee >/dev/null 2>&1 || { echo "Error: lychee not found. Install it from https://github.com/lycheeverse/lychee#installation"; exit 1; }
	npm install
	rm -rf public
	PATH="$(CURDIR)/node_modules/.bin:$$PATH" hugo --gc --minify --environment production -b https://porch.kpt.dev/ --destination public
	lychee -vv --config lychee.toml --root-dir public 'public/**/*.html'
