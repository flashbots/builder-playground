# Repository Guidelines

## Project Structure & Module Organization
- `main.go` exposes the `builder-playground` CLI; Go packages sit alongside it with Go modules declared in `go.mod`.
- `playground/` owns orchestration code: recipe definitions (`recipe_l1.go`, `recipe_opstack.go`), manifest/artifact builders, and helper utilities. Tests sit next to the source, with shared fixtures under `playground/testcases/`.
- `docs/` provides reference material (`architecture.md`, `internals/`, `recipes/`), while `examples/` documents opinionated run books.
- Supporting assets live in dedicated folders: `assertoor/`, `cl-proxy/`, `mev-boost-relay/`, `healthmon/`, `scripts/`, and `tools/`.

## Build, Test, and Development Commands
- `make build` → compile the CLI with version metadata and drop `./builder-playground`.
- `go run main.go start <recipe>` → launch a stack directly (e.g., `go run main.go start l1 --latest-fork`).
- `make test` → run `go test -v ./...`; `make integration-test` → run recipe-level e2e tests in `playground/` with `INTEGRATION_TESTS=true`.
- `make lint` → `gofmt`, `gofumpt -extra`, `go vet`, `staticcheck`; `make fmt` → formatting pass plus `gci write .` and `go mod tidy`.
- `make generate-docs` → rebuild `docs/recipes/*.md` after touching flags, manifests, or templates.

## Coding Style & Naming Conventions
- Target Go 1.25.1 with gofmt-style tabs and mixedCaps identifiers; exported symbols need doc comments.
- Keep filenames consistent: `recipe_<name>.go`, `cmd_<feature>.go`, `_test.go` for tests, `.tmpl` for config templates.
- Let the provided tooling manage imports (`gci`) and formatting (`gofmt` + `gofumpt`); avoid manual fixes that fight the linters.

## Testing Guidelines
- New logic requires companion `_test.go` files near the code. Favor table-driven tests and store reusable fixtures under `playground/testcases/`.
- Fast feedback: `go test ./playground/... -run TestRecipe` or `go test ./... -run TestComponent`. Seed randomness for deterministic Docker interactions.
- Extend `make integration-test` coverage when recipes, flags, or container wiring changes; document any prerequisites in `docs/recipes/`.

## Commit & Pull Request Guidelines
- Follow the existing imperative subject style with optional issue references, e.g., `Fix updateTaskStatus panic (#316)`.
- Each PR should explain scope, validation commands (`make test`, `builder-playground start opstack`, etc.), and linked docs/examples updates. Provide logs or screenshots when behavior changes.
- Keep commits focused (code, generated docs, manifests as separate commits).

## Environment & Configuration Tips
- Ensure Docker Desktop (or compatible) is running before launching recipes.
- Default manifests live in `playground/config.yaml.tmpl`. Copy and adapt this template for new recipes, then document knobs in `docs/recipes/<recipe>.md`.
- Use the prefunded keys listed in `README.md` for testing; avoid committing custom secrets—pass overrides via CLI flags like `--prefunded-accounts`.
