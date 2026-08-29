# Security Policy

## Reporting a vulnerability

Please report vulnerabilities privately via
[GitHub private vulnerability reporting](https://github.com/The127/beanstore/security/advisories/new)
— do not open a public issue for security problems.

You can expect an acknowledgement within a week. Please include enough
detail to reproduce the issue (affected component, setup, steps).

There is no bug bounty program.

## Supported versions

beanstore is in early development and has no releases yet; only the `main`
branch is supported. Once versioned releases exist, this section will state
which release lines receive security fixes.

## Scope notes

beanstore is a per-node storage daemon that manages LVM volumes and moves
volume data between nodes. Of particular interest are issues in:

- the node-to-node move protocol (authentication, integrity of streamed
  extents, the commit/outcome-resolution rules),
- the gRPC control API (authentication bypass, state-machine violations
  that break the single-authoritative-copy invariant),
- privilege handling around LVM operations.
