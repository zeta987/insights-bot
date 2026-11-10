# Repository Guidelines

## Project Structure & Module Organization
- `cmd/insights-bot/` contains the main entrypoint for the bot binary.
- `internal/` hosts core application logic (bots, services, configs, datastore).
- `pkg/` provides reusable packages (i18n, logging, utils, health checks).
- `ent/` is generated ORM code; schemas live in `ent/schema/`.
- `locales/` holds translation files (e.g., `en.yaml`, `zh-CN.yaml`).
- `docs/` contains documentation assets; `production/` includes deployment helpers.

## Build, Test, and Development Commands
- `go build -a -o build/insights-bot github.com/nekomeowww/insights-bot/cmd/insights-bot` builds a local binary.
- `go build -a -o release/insights-bot github.com/nekomeowww/insights-bot/cmd/insights-bot` creates a release artifact.
- `docker compose --profile hub up -d` runs the prebuilt image.
- `docker compose --profile local up -d --build` builds and runs from local code.
- `docker buildx build --platform linux/arm64,linux/amd64 -t <tag> -f Dockerfile .` builds multi-arch images.

## Coding Style & Naming Conventions
- Language: Go; use `gofmt` on all Go files.
- Avoid hand-editing `ent/` generated files; update schemas and re-run codegen.
- Prefer descriptive package names and keep exported identifiers in `CamelCase`.

## Testing Guidelines
- Tests are Go `*_test.go` files (e.g., `internal/models/chathistories/recap_test.go`).
- Run all tests with `go test ./...`.
- If you add new logic, add or extend tests in the nearest package.

## Commit & Pull Request Guidelines
- Commit messages follow Conventional Commits: `feat:`, `fix:`, `chore:` with optional scopes (e.g., `chore(deps): ...`). `[i18n] ...` is also used for translation updates.
- PRs should include a concise summary, testing notes (`go test ./...`, docker profile used), and any config changes.
- For behavior changes, mention impacted bots or commands (e.g., `/recap_forwarded`).
- Committing is encouraged, but pushing is opt-in; see "Commit & Push Policy" and "Remote & PR Policy" below before running `git push` or any `gh pr` command.

## Security & Configuration Tips
- Copy `.env.example` to `.env` for local runs; never commit secrets.
- Key ports: 6060 (pprof), 7069 (health), 7070–7072 (bot webhooks).
- Regenerate ORM after schema changes: `go generate ./ent`.

## Vibe Workflow: Verify on Telegram Before Moving On
- After completing a change set that affects recap or messaging behavior, run the bot in DEV mode in the foreground so the developer can watch the result in Telegram.
- Prerequisites: local Postgres and Redis must be up, and `.env` must be present in the repo root. Then run `go run github.com/nekomeowww/insights-bot/cmd/insights-bot` from the repo root.
- With `AUTO_RECAP_TEST_ENABLED=1` and `AUTO_RECAP_TEST_CHAT_ID` set, startup immediately triggers one auto-recap to the test chat (see the `NewAutoRecapService` DEBUG block in `internal/services/autorecap/autorecap.go`).
- The agent MUST pause after starting the dev run and wait for explicit developer confirmation of the Telegram result before continuing with further changes, merges, or releases.

## Commit & Push Policy
- Commit early and often with Conventional Commits; every logical unit of work gets its own commit.
- Do NOT push by default. Pushing risks leaking private data; push ONLY when the developer explicitly asks (e.g., for a release).
- Before any push, re-verify that the outgoing commits contain no secrets or personally identifiable information.

## Remote & PR Policy
- `origin` = `git@github.com:zeta987/insights-bot.git` (the maintainer's fork). `upstream` = `git@github.com:nekomeowww/insights-bot.git`.
- NEVER open pull requests against upstream, never push to upstream, and never target `upstream/main` in any `gh` command. `gh` defaults can resolve to upstream — always pass `--repo zeta987/insights-bot` explicitly.
- Integration is local-first: merge local branches directly (feature -> `main`; `main` -> `production` when the maintainer says it is stable), or at most open a PR within the origin fork (e.g., feature branch -> own `main`).

## Branch & Deployment Flow
- Two long-lived branches only: `main` and `production`. There is no `develop` branch; do not recreate it.
- `main` is the development branch and the GitHub default. Day-to-day work lands here, directly or through short-lived `feat/`, `fix/`, or `dev/` branches that are deleted after merging. Pushing `main` runs CI and deploys nothing.
- `production` is what runs live. The deployment platform auto-deploys on every push to `production`, so a push there is a release to the real bot.
- Going live: verify the change on Telegram with the local dev run first, then fast-forward `production` to the tested `main` commit (`git push origin main:production`). Never commit directly on `production` and never force-push it; it must stay an ancestor of `main`.
- Pushing `production` needs the maintainer's explicit request each time, the same as any other push.

## Maintainer-Local Notes
- Machine-specific details (deployment target, test tunnel, local services) live in `AGENTS.local.md` at the repo root. The file is gitignored; read it when it exists and never commit its contents.

## Worktree Protocol
- Parallel tasks run in `.claude/worktrees/*` on `claude/*` branches.
- When an agent finishes its worktree task it must: run `gofmt` + build + relevant tests, commit its work on its branch, and report a summary back (what changed, test results, open questions). It must NOT merge or push on its own.
- After all worktree tasks complete, the coordinating session reviews every worktree diff, merges approved branches into the feature branch, re-runs the test suite, and prunes the worktrees.

## Release Protocol
- Releases happen only after a feature milestone AND developer confirmation via the dev run described in "Vibe Workflow: Verify on Telegram Before Moving On".
- Steps: update `CHANGELOG.md` (zh-TW + English, AyuGram-style sections), tag `vX.Y.Z`, push the branch and tag to `origin` ONLY, and publish a normal (non-prerelease) GitHub release on `zeta987/insights-bot` with bilingual notes matching the changelog.
