# beanstore

LVM-based per-node storage API (thin LVs, detached-only moves, no internal
coordinator). Standalone tool — not part of any orchestrator; no
orchestrator types or assumptions in the API.

The contract lives at `docs/CONTRACT.md`. Code conforms to the contract,
not the other way around.

## Working agreements

- Commit messages follow plain Conventional Commits: `type: description`
  (`feat`, `fix`, `chore`, `docs`, `refactor`, `test`, ...). No scopes.
- Every commit is DCO signed off: always `git commit -s`. Enforced by the
  commit-msg hook.
- Work incrementally in small reviewed steps — no big-bang generation.
- Use the justfile: `just ci` (lint + build + test over both modules) must
  pass before a commit; `just hooks` activates the git hooks after cloning.

## AI usage rules

These are enforced for all AI-assisted work in this repo (see README /
CONTRIBUTING for the contributor-facing policy):

- Every commit containing AI-generated code carries a co-author trailer,
  e.g. `Co-Authored-By: Claude <noreply@anthropic.com>`.
- Commits do NOT carry session links or other tool-run metadata (no
  `Claude-Session:` trailers) — this repo overrides that default.
- The human contributor stays responsible: AI agents propose changes and
  commit only after the contributor has reviewed, understood, and approved
  them. The gate is at the commit, not the push.

## Licensing boundary

The server (repo root module) is AGPL-3.0; `client/` is a separate
MIT-licensed Go module holding the protos, generated stubs, and error
taxonomy. The server may import the client module, never the reverse. New
API surface goes on the MIT side.

## Personal extensions

Machine- or person-specific notes go in `CLAUDE.local.md` (untracked):

@CLAUDE.local.md
