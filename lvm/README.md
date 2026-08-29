# lvm

Go client for lvm2, wrapping the `lvm` command line, which is the stable
programmatic interface since lvm2 removed liblvm2app. Requires lvm2
2.02.158 (2016) or newer for json reports. MIT licensed, part of the
[beanstore](https://github.com/The127/beanstore) repository.

## Supported operations

| Go                              | lvm                 |
| ------------------------------- | ------------------- |
| `CreatePhysicalVolume`          | `pvcreate`          |
| `ListPhysicalVolumes`           | `pvs`               |
| `RemovePhysicalVolume`          | `pvremove`          |
| `AddPhysicalVolumeTag`          | `pvchange --addtag` |
| `RemovePhysicalVolumeTag`       | `pvchange --deltag` |
| `SetPhysicalVolumeAllocatable`  | `pvchange -x`       |
| `RegeneratePhysicalVolumeUUID`  | `pvchange -u`       |
| `SetPhysicalVolumeMetadataIgnore` | `pvchange --metadataignore` |
| `ResizePhysicalVolume`          | `pvresize`          |
| `ResizePhysicalVolumeTo`        | `pvresize --setphysicalvolumesize` |

## Usage

```go
client := lvm.New()

err := client.CreatePhysicalVolume(ctx, "/dev/sdb")
if err != nil {
	return err
}

pvs, err := client.ListPhysicalVolumes(ctx)
if err != nil {
	return err
}

for _, pv := range pvs {
	fmt.Printf("%s: %d bytes free\n", pv.Device, pv.FreeBytes)
}
```

## Notes

- lvm needs read and write access to the target block devices, to the
  device-mapper control node `/dev/mapper/control`, and to its lock and
  metadata directories under `/run/lock/lvm` and `/etc/lvm`. Run the
  process with those permissions or route commands through sudo.
- Hosts using the lvm devices file hide untracked devices from most
  commands, while `pvcreate` silently adds its target to the file.
  `lvm.New(lvm.WithDevices("/dev/loop0"))` scopes a client to explicit
  devices, leaving the devices file untouched.

## License

[MIT](LICENSE)
