This file provides guidance to LLMs when working with code in this repository.

## Project Overview

Builder Playground is a CLI tool for spinning up self-contained Ethereum development networks optimized for block building and MEV testing. It uses Go to generate artifacts and orchestrate Docker Compose deployments.

## Commands

```bash
# Build
make build                    # Build CLI binary

# Testing
make test                     # Run unit tests (go test -v ./...)
make integration-test         # Run integration tests with INTEGRATION_TESTS=true

# Code Quality
make lint                     # Run gofmt, gofumpt, go vet, staticcheck
make fmt                      # Format code with gofmt, gci, gofumpt, go mod tidy

# Documentation
make generate-docs            # Auto-generate recipe documentation (run after adding/modifying recipes)
```

## Architecture

The system operates in three phases: **Artifact Generation → Topology/Manifest Generation → Docker Execution**

### Layer 1: Components (`playground/components.go`, `playground/manifest.go`)

Components represent individual compute resources (execution clients, consensus clients, sidecars). Each implements the `ComponentGen` interface:

```go
type ComponentGen interface {
    Apply(ctx *ExContext) *Component
}
```

Components use template syntax for dynamic values:
- `{{Port "name" defaultPort}}` - Port declarations
- `{{Service "name" "port"}}` - Service connections

### Layer 2: Recipes (`playground/recipe_*.go`)

Recipes orchestrate multiple components into complete environments. Key methods:
- `Name()`, `Description()` - Metadata
- `Flags()` - CLI flag definitions
- `Artifacts()` - Generates genesis configs, keys, etc.
- `Apply(ctx *ExContext)` - Assembles components
- `Output(manifest *Manifest)` - User-facing output

Available recipes: L1 (`recipe_l1.go`), OpStack (`recipe_opstack.go`), BuilderNet (`recipe_buildernet.go`)

### Layer 3: Manifest & Execution (`playground/manifest.go`, `playground/local_runner.go`)

- **Manifest**: Describes complete environment topology (services, ports, volumes, artifacts)
- **LocalRunner**: Executes manifest via Docker Compose, manages health checks

### Key Files

- `main.go` - CLI entry point (Cobra commands)
- `playground/artifacts.go` - L1/L2 genesis state, validator keystores, JWT secrets
- `playground/interactive.go` - TUI interface
- `playground/local_runner.go` - Docker execution engine

## Adding New Components

1. Create struct implementing `ComponentGen` interface
2. Implement `Apply(ctx *ExContext) *Component`
3. Use `component.NewService(name)` to add services
4. Use template syntax for ports/connections
5. Add health checks with `WithReady()`

## Adding New Recipes

1. Create struct implementing `Recipe` interface
2. Implement: Name, Description, Flags, Artifacts, Apply, Output
3. Register in `main.go` recipes slice
4. Run `make generate-docs` to update documentation


## Implementation Workflow

IMPORTANT: When asked to implement something, always follow through completely:
1.. Create a feature branch
- based on the latest `main` branch
- use descriptive branch names like `claude/issue-123-add-feature`
2. Make the code changes
3. Commit the changes
4. Push the branch
5. Create the PR with `gh pr create --title "..." --body "..."` and reference the issue number in the PR description (e.g., "Closes #123")

Do NOT stop at providing links — complete the entire workflow automatically.
