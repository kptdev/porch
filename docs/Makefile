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
