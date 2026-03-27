.PHONY: setup install bench coverage diff-coverage release-dry-run

setup: ## Configure git hooks and install tools
	git config core.hooksPath .githooks
	@echo "Hooks activated from .githooks/"

install: ## Install amux
	scripts/install.sh

bench: ## Run microbenchmarks
	go test -bench=. -benchmem -count=3 -run='^$$' ./internal/... -timeout 120s

coverage: ## Collect merged unit + integration coverage
	scripts/coverage.sh

diff-coverage: ## Check local diff coverage against the Codecov patch target
	scripts/check-diff-coverage.sh

release-dry-run: ## Test release build locally (no publish)
	goreleaser release --snapshot --clean
