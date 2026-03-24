.PHONY: setup install bench coverage release-dry-run

setup: ## Configure git hooks and install tools
	git config core.hooksPath .githooks
	@echo "Hooks activated from .githooks/"

install: ## Build and install amux
	scripts/build-install.sh

bench: ## Run microbenchmarks
	go test -bench=. -benchmem -count=3 -run='^$$' ./internal/... -timeout 120s

coverage: ## Collect merged unit + integration coverage
	scripts/coverage.sh

release-dry-run: ## Test release build locally (no publish)
	goreleaser release --snapshot --clean
