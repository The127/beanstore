# lvm

A Go library for lvm2, built on the lvm command line interface with JSON
reporting. Since lvm2 removed liblvm2app, the CLI is the stable
programmatic interface lvm2 offers, and this library wraps it with typed
results, exact command construction, and testability.

Part of [beanstore](https://github.com/The127/beanstore) but MIT licensed
as its own module, usable by any project.

## Install

```bash
go get github.com/The127/beanstore/lvm
```

## Usage

```go
client := lvm.New()

err := client.CreateThinVolume(ctx, "vg0", "pool0", "vol1", 1<<30, "state.creating")

volumes, err := client.ListVolumes(ctx, "vg0")

err = client.AddTag(ctx, "vg0", "vol1", "state.ready")
err = client.RemoveTag(ctx, "vg0", "vol1", "state.creating")

err = client.RemoveVolume(ctx, "vg0", "vol1")
```

The process needs permission to run the `lvm` binary, which in practice
means root or an equivalent capability set.

## Testing

Commands run through the `Runner` interface. Inject your own with
`lvm.NewWithRunner` to fake lvm in tests, or to route commands through
sudo, a container, or a remote shell:

```go
type Runner interface {
    Run(ctx context.Context, args ...string) ([]byte, error)
}
```

## License

[MIT](LICENSE)
