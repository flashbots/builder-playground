# Heavily inspired by Lighthouse: https://github.com/sigp/lighthouse/blob/stable/Makefile
# and Reth: https://github.com/paradigmxyz/reth/blob/main/Makefile
.DEFAULT_GOAL := help

VERSION := $(shell git describe --tags --always --dirty="-dev")

##@ Help

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "Usage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

.PHONY: v
v: ## Show the version
	@echo "Version: ${VERSION}"

##@ Build

.PHONY: build
build: ## Build the CLI
	go build -ldflags "-X main.version=${VERSION}" -o ./builder-playground main.go
	@echo "Binary built: ./builder-playground (version: ${VERSION})"

##@ Test & Development

.PHONY: test
test: ## Run tests
	go test ./...


.PHONY: lint
lint: ## Run linters
	gofmt -d -s .
	gofumpt -d -extra .
	go vet ./...
	staticcheck ./... || true
	# golangci-lint run || true

.PHONY: fmt
fmt: ## Format the code
	gofmt -s -w .
	gci write .
	gofumpt -w -extra .
	go mod tidy

.PHONY: gofumpt
gofumpt: ## Run gofumpt
	gofumpt -l -w -extra .

.PHONY: lt
lt: lint test ## Run linters and tests

.PHONY: ci-release
ci-release:
	docker run \
		--rm \
		-e CGO_ENABLED=1 \
		-e GITHUB_TOKEN="$(GITHUB_TOKEN)" \
		-v /var/run/docker.sock:/var/run/docker.sock \
		-v $(HOME)/.docker/config.json:/root/.docker/config.json \
		-v `pwd`:/go/src/$(PACKAGE_NAME) \
		-v `pwd`/sysroot:/sysroot \
		-w /go/src/$(PACKAGE_NAME) \
		ghcr.io/goreleaser/goreleaser-cross:v1.21.12 \
		release --clean --auto-snapshot
