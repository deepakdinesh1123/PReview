# AGENTS.md

Guidance for AI coding agents (and humans) working on PReview.

## What this project is

PReview deploys a pull request / branch to an **AWS Lambda MicroVM** and posts the endpoint URL on the PR. Single Go binary (`preview`) used strictly as a composite GitHub Action. Modelled on [pullpreview/action](https://github.com/pullpreview/action) (label-driven, marker-based PR comment). Read [`docs/architecture.md`](docs/architecture.md) before changing behaviour and [`docs/setup.md`](docs/setup.md) for user-facing setup.

## Layout

```
cmd/main.go               kong CLI tree, logger, AWS config, exit codes
internal/preview/         commands + orchestration (deploy.go is the core)
internal/cloud/           thin AWS SDK wrappers (lambda.go, s3.go) - no business logic
internal/github/          event parsing + Decide(), PR comment client
action.yml                composite Action (builds the CLI from source)
examples/preview.yml      consumer workflow
docs/                     architecture + setup
```

## Commands

```bash
go build ./...          # or: just build  -> dist/preview
go vet ./... && gofmt -l .
go test ./...           # all unit tests; no AWS or network needed
```

CI (`.github/workflows/ci.yml`) runs gofmt check, vet and tests. Keep them green.

## Invariants - do not break these

1. **No local state.** A deployment is identified only by `ImageName(prefix, key)` (`internal/preview/names.go`). Changing the naming scheme orphans existing deployments; treat it as a breaking change.
2. **Prefix scoping is a safety boundary.** Teardowns must only touch names starting with the name prefix.
3. **Never deploy fork PRs.** `github.Decide` ignores them; fork PRs have no secrets and must never run with credentials.
4. **Comment failures must not fail a deploy**, and **deploy/teardown failures must exit non-zero** (`kctx.FatalIfErrorf`).
5. **Teardown is idempotent**; **deploy swaps run-then-retire** (new VM running before old ones are terminated).
6. **Wait on the image *version***, not the image state, after create/update.
7. **Action inputs go through env vars**, never `${{ }}` inside the shell script (injection safety).
8. **`internal/cloud` stays a thin wrapper.** Orchestration and policy belong in `internal/preview`; decisions about events belong in `internal/github` as pure functions.

## Conventions

- Add new flags to `CommonFlags` with `name:`, `help:` and an `env:"PREVIEW_*"`; mirror them as Action inputs in `action.yml` and in the `docs/setup.md` table.
- Prefer pure, table-driven unit tests (see `internal/github/github_test.go`). New AWS behaviour should hide behind a small seam so logic can be tested without credentials.
- Wrap errors with context (`fmt.Errorf("...: %w", err)`); never dereference SDK pointers directly - use `aws.ToString` etc.
- Keep comments/docstrings; document exported identifiers.
- Never commit secrets, `.env`, account IDs, or built binaries (`dist/` is ignored).

## Known gaps (good first tasks)

- No integration test against real AWS; first live deploy is unverified (see "Known limitations" in the architecture doc).
- App env vars/secrets are not injected into the MicroVM yet (`EnvironmentVariables` on the image API is the hook).
- Lifecycle hooks (`Ready`/`Run`) are not configured.
- Optional GitHub Deployments API / commit status integration (pullpreview reports status too).
- Release-binary download in `action.yml` to avoid building from source each run.
