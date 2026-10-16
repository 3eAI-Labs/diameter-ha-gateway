# Diameter HA Gateway — developer tasks.
#
# The frontend build is hermetic: the admin UI's component library is vendored into
# web/vendor-ui-kit/, so `docker build` needs no other checkout.
#
# Go targets run with GOWORK=off: a go.work in a parent directory would otherwise
# change which modules the build resolves.

export GOWORK := off

.DEFAULT_GOAL := help

.PHONY: help build vet test licenses image

help: ## List available targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

build: ## Build the gateway binary into bin/
	CGO_ENABLED=0 go build -trimpath -o bin/diameter-gateway ./cmd/diameter-gateway

vet: ## go vet the gateway
	go vet ./...

test: ## Run the gateway tests with the race detector
	go test -race -count=1 ./...

licenses: ## Check licence headers and dependency licences
	scripts/check-licenses.sh

image: ## Build the container image (admin UI, gateway, licence texts) as diameter-ha-gateway:dev
	docker build -f deployments/Dockerfile -t diameter-ha-gateway:dev .

# Targets that need files outside the public tree (see internal.mk).
-include internal.mk
