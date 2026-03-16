.PHONY: setup build test bench coverage

setup: ## Configure git hooks and install tools
	git config core.hooksPath .githooks
	@echo "Hooks activated from .githooks/"

build: ## Build and install amux
	go build -o ~/.local/bin/amux .

test: ## Run all tests
	go test ./... -timeout 120s

bench: ## Run microbenchmarks
	go test -bench=. -benchmem -count=3 -run='^$$' ./internal/... -timeout 120s

coverage: ## Collect merged unit + integration coverage
	scripts/coverage.sh
