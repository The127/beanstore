# Releasing

Beanstore is a multi module repo, releases tag all three modules on
the same commit:

```
git tag -s client/v0.1.0
git tag -s lvm/v0.1.0
git tag -s v0.1.0
git push origin client/v0.1.0 lvm/v0.1.0 v0.1.0
```

The `v*` tag triggers the release workflow: goreleaser builds the
daemon for linux amd64 and arm64, packages deb and rpm (systemd unit,
example config, lvm2 dependency) and publishes a GitHub release with
a changelog grouped by conventional commit type. The `client/v*` and
`lvm/v*` tags only version the Go modules for consumers.

The server module keeps its `replace` directives for the sibling
modules. It is a packaged binary, not an importable library, so `go
install` support is not a goal.

The daemon binary reports its version from the build info stamp, a
clean checkout of the tag is enough, no ldflags.

Verify the config before tagging:

```
just release-check
just release-snapshot
```
