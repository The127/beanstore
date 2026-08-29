# beanstore

LVM-based per-node storage API (thin LVs, detached-only moves, no internal
coordinator). Standalone tool, not part of any orchestrator. No
orchestrator types or assumptions in the API.

The contract lives at `docs/CONTRACT.md`. Code conforms to the contract,
not the other way around.

## Working agreements

- Commit messages follow plain Conventional Commits: `type: description`
  (`feat`, `fix`, `chore`, `docs`, `refactor`, `test`, ...). No scopes.
- Every commit is DCO signed off: always `git commit -s`. Enforced by the
  commit-msg hook.
- Work incrementally in small reviewed steps, no big-bang generation.
- Keep comments short. Never explain what the code does, the code is the
  documentation (it cannot go stale). Comment only the why, and only when
  it is non-obvious.
- No em-dashes and no semicolons in prose: documents, comments, and commit
  messages. Semicolons in code only where Go syntax requires them.
- Package doc comments live in a dedicated `doc.go` per package, never on
  another file's package clause.
- Use the justfile: `just ci` (lint + build + test over both modules) must
  pass before a commit. `just setup` prepares a fresh clone.

## AI usage rules

These are enforced for all AI-assisted work in this repo (see README /
CONTRIBUTING for the contributor-facing policy):

- Every commit containing AI-generated code carries a co-author trailer,
  e.g. `Co-Authored-By: Claude <noreply@anthropic.com>`.
- Commits do NOT carry session links or other tool-run metadata (no
  `Claude-Session:` trailers). This repo overrides that default.
- The human contributor stays responsible: AI agents propose changes and
  commit only after the contributor has reviewed, understood, and approved
  them. The gate is at the commit, not the push.

## Licensing boundary

The server (repo root module) is AGPL-3.0. Two separate MIT-licensed Go
modules exist for consumers: `client/` (protos, generated stubs, error
taxonomy) and `lvm/` (the Go lvm2 library). The server may import the MIT
modules, never the reverse. New wire API surface goes into `client/`.

## Personal extensions

Machine- or person-specific notes go in `CLAUDE.local.md` (untracked):

@CLAUDE.local.md
