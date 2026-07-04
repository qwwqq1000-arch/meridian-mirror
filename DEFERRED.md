# Deferred Items

Items identified during architectural refactor planning that are intentionally deferred to separate PRs.

## Tooling & Config
1. **Biome linting/formatting** — Add with clean project-specific config (not copy-pasted). Separate PR.
2. **Dependency classification fix** — Move `hono`, `@hono/node-server` from devDeps to production deps.
3. **Docker directory reorganization** — Move Docker files to `docker/`. Needs migration docs.
4. **`src/index.ts` barrel export** — Single entry point for npm consumers. Needs backwards-compat analysis.

## Deprecation Paths
5. **`bin/oc.sh` deprecation** — Evaluate whether to deprecate. Needs user communication first.
6. **`claude-max-headers.ts` plugin deprecation** — Needs migration path for users who have it configured.

## Feature Enhancements
7. **`prepareMessages` / prompt builder extraction** — Centralize Anthropic messages → text prompt conversion. Fits into adapter pattern as `preparePrompt()`.
8. **`maxTurns` configurability** — Currently hardcoded to 200. Should be configurable via env var or adapter config.
