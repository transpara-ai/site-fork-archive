.PHONY: css generate-personas generate build test vet verify-go verify verify-canonical-paths verify-public-shell-clean run dev deploy

NODE_BIN ?= $(shell if [ -d "$(HOME)/.nvm/versions/node" ]; then latest=$$(find "$(HOME)/.nvm/versions/node" -mindepth 1 -maxdepth 1 -type d -name 'v*' | sort -V | tail -n 1); if [ -n "$$latest" ]; then printf '%s/bin' "$$latest"; fi; fi)
TOOLS_PATH := $(HOME)/go/bin:$(HOME)/.local/bin$(if $(NODE_BIN),:$(NODE_BIN),):/snap/bin
export PATH := $(TOOLS_PATH):$(PATH)

GO ?= go
NPM ?= npm
TEMPL ?= templ
TAILWIND ?= ./node_modules/.bin/tailwindcss
SITE_REVISION ?= $(shell git rev-parse --verify HEAD 2>/dev/null)
SITE_LDFLAGS := -X github.com/transpara-ai/site/internal/buildinfo.embeddedRevision=$(SITE_REVISION)

LEGACY_OPERATION_REPO := transpara-ai/civilization-operation

node_modules/.site-deps.stamp: package.json package-lock.json
	$(NPM) ci
	touch node_modules/.site-deps.stamp

css: node_modules/.site-deps.stamp
	$(TAILWIND) -i ./static/css/input.css -o ./static/css/site.css --minify

generate-personas:
	$(GO) generate ./graph/personas/...

generate: generate-personas
	$(TEMPL) generate

build: css generate
	@test -n "$(strip $(SITE_REVISION))" || { echo "SITE_REVISION is required when Git revision is unavailable" >&2; exit 1; }
	$(GO) build -ldflags "$(SITE_LDFLAGS)" -o site ./cmd/site/

test:
	$(GO) test -count=1 ./...

vet:
	$(GO) vet ./...

verify-go: generate test vet

verify-canonical-paths:
	test -f graph/review_console.go
	grep -Fq 'https://github.com/transpara-ai/operation/issues/26' graph/review_console.go
	grep -Eq 'SourceRepo:[[:space:]]+"transpara-ai/operation"' graph/review_console.go
	@# Keep scanning narrow so historical docs and submodule history remain preserved.
	@output=$$(grep -RnF "$(LEGACY_OPERATION_REPO)" graph/review_console.go); status=$$?; \
	if [ $$status -eq 0 ]; then \
		printf '%s\n' "$$output"; \
		echo "canonical-repo violation: use transpara-ai/operation"; \
		exit 1; \
	fi; \
	if [ $$status -ne 1 ]; then \
		echo "canonical-repo scan failed"; \
		exit $$status; \
	fi

verify-public-shell-clean:
	bash scripts/verify-public-shell-clean.sh

verify: verify-canonical-paths build verify-public-shell-clean test vet

run: build
	./site

dev: node_modules/.site-deps.stamp
	$(TAILWIND) -i ./static/css/input.css -o ./static/css/site.css --watch &
	$(TEMPL) generate --watch &
	$(GO) run ./cmd/site/

# on-prem deploy — private to Transpara-AI; build + restart the systemd user service (see deploy.sh)
deploy:
	./deploy.sh
