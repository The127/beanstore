# beanstore

[![ci](https://github.com/The127/beanstore/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/The127/beanstore/actions/workflows/ci.yml)
[![coverage](https://codecov.io/gh/The127/beanstore/graph/badge.svg)](https://app.codecov.io/gh/The127/beanstore)
[![server license: AGPL-3.0](https://img.shields.io/badge/server-AGPL--3.0-blue)](LICENSE)
[![client license: MIT](https://img.shields.io/badge/client-MIT-green)](client/LICENSE)
[![lvm license: MIT](https://img.shields.io/badge/lvm-MIT-green)](lvm/LICENSE)

An LVM-based per-node storage API. beanstore manages thin logical volumes on
the node it runs on and moves detached volumes between nodes. Nothing more.

> **Status: early development.** Nothing here is usable yet. The design
> contract is settled before code, and the code follows it.

## Design

- Volumes are LVM thin LVs. A node is authoritative about its own disk.
- Moves require the volume to be detached. There is no live move.
- No coordinator inside beanstore. An orchestrator (any orchestrator)
  drives the API, nodes enforce the rules. All central state is a
  rebuildable cache: scanning all nodes yields truth.
- Crash recovery is resolved from on-disk state plus a commit protocol
  between the two nodes of a move. At most one node ever holds an
  authoritative copy of a volume.
- Verbs are strict-state: a verb in the wrong state is refused, never
  absorbed. Idempotency and retries are the calling driver's job.

## Usage

Not yet. The gRPC API surface (volume lifecycle, attach/detach, the move
protocol, and the scan) will be documented here once it exists.

## Contributing

Contributions are welcome, see [CONTRIBUTING.md](CONTRIBUTING.md). In
short: commits follow plain Conventional Commits (`type: description`, no
scopes) and must be DCO signed off (`git commit -s`). Both rules are
enforced by git hooks.

## AI-assisted contributions

AI-assisted contributions need to follow these rules:

- **Disclose it.** Commits with AI-generated code carry a co-author
  trailer, e.g. `Co-Authored-By: Claude <noreply@anthropic.com>`.
- **You are the author.** The contributor is fully responsible for
  AI-generated code: its correctness, its license compatibility, and the
  DCO sign-off certifying the right to submit it. "The AI wrote it" is
  never an excuse.
- **Review it yourself, before pushing.** Submit only code you have read,
  understood, and could explain and defend in review as your own.

## Development setup

Required tooling:

- [Go](https://go.dev/) 1.25+: the repo holds two modules, the server at
  the root and the client library in `client/`.
- [golangci-lint](https://golangci-lint.run/) v2: linting, configured in
  `.golangci.yml`.
- [buf](https://buf.build/): protobuf linting and code generation
  (`just proto-lint`, `just proto`).
- [just](https://just.systems/): task runner. `just` lists the available
  recipes, `just ci` runs everything that must pass.
- [lefthook](https://lefthook.dev/): git hooks (lint on pre-commit,
  commit message and sign-off checks on commit-msg).

After cloning, run the one-time setup. It creates the local (untracked)
go workspace, so the server builds against your working-tree client, and
activates the git hooks:

```bash
just setup
```

## License

- The beanstore server (this repository's root module) is licensed under
  [AGPL-3.0](LICENSE).
- The Go client library in [`client/`](client/) (protobuf definitions,
  generated stubs, and the error taxonomy) is a separate module licensed
  under [MIT](client/LICENSE), so consumers can link it without copyleft
  obligations. Talking to a beanstore daemon over the wire carries no
  license obligations either way.
- The Go lvm2 library in [`lvm/`](lvm/) is likewise a separate module
  licensed under [MIT](lvm/LICENSE), usable on its own by any project
  that needs to talk to lvm2 from Go.
