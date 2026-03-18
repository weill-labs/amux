.PHONY: setup build test bench coverage release-dry-run

setup: ## Configure git hooks and install tools
	git config core.hooksPath .githooks
	@echo "Hooks activated from .githooks/"

build: ## Build and install amux
	scripts/build-install.sh

test: ## Run all tests
	go test ./... -timeout 120s

bench: ## Run microbenchmarks
	go test -bench=. -benchmem -count=3 -run='^$$' ./internal/... -timeout 120s

coverage: ## Collect merged unit + integration coverage
	scripts/coverage.sh

release-dry-run: ## Test release build locally (no publish)
	goreleaser release --snapshot --clean
