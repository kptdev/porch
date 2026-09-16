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

.PHONY: check-links-external
check-links-external:
	@command -v hugo >/dev/null 2>&1 || { echo "Error: hugo not found. See https://gohugo.io/installation/"; exit 1; }
	@command -v npm >/dev/null 2>&1 || { echo "Error: npm not found. See https://nodejs.org/"; exit 1; }
	@command -v lychee >/dev/null 2>&1 || { echo "Error: lychee not found. Install it from https://github.com/lycheeverse/lychee#installation"; exit 1; }
	npm install
	rm -rf public
	PATH="$(CURDIR)/node_modules/.bin:$$PATH" hugo --gc --minify --environment production -b https://porch.kpt.dev/ --destination public
	lychee -vv --config lychee.toml --root-dir public 'public/**/*.html'
