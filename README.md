# beanstore

An LVM-based per-node storage API. beanstore manages thin logical volumes on
the node it runs on and moves detached volumes between nodes — nothing more.

> **Status: early development.** Nothing here is usable yet; the design
> contract is settled before code, and the code follows it.

## Design

- Volumes are LVM thin LVs. A node is authoritative about its own disk.
- Moves require the volume to be detached. There is no live move.
- No coordinator inside beanstore. An orchestrator (any orchestrator)
  drives the API; nodes enforce the rules. All central state is a
  rebuildable cache — scanning all nodes yields truth.
- Crash recovery is resolved from on-disk state plus a commit protocol
  between the two nodes of a move; at most one node ever holds an
  authoritative copy of a volume.
- Verbs are strict-state: a verb in the wrong state is refused, never
  absorbed. Idempotency and retries are the calling driver's job.

## Usage

Not yet. The gRPC API surface (volume lifecycle, attach/detach, the move
protocol, and the scan) will be documented here once it exists.

## License

- The beanstore server (this repository's root module) is licensed under
  [AGPL-3.0](LICENSE).
- The Go client library in [`client/`](client/) — protobuf definitions,
  generated stubs, and the error taxonomy — is a separate module licensed
  under [MIT](client/LICENSE), so consumers can link it without copyleft
  obligations. Talking to a beanstore daemon over the wire carries no
  license obligations either way.
