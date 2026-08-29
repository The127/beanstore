# lvm

Go client for lvm2, wrapping the `lvm` command line, which is the stable
programmatic interface since lvm2 removed liblvm2app. Requires lvm2
2.02.158 (2016) or newer for json reports. MIT licensed, part of the
[beanstore](https://github.com/The127/beanstore) repository.

## Supported operations

| Go                     | lvm        |
| ---------------------- | ---------- |
| `CreatePhysicalVolume` | `pvcreate` |
| `ListPhysicalVolumes`  | `pvs`      |
| `RemovePhysicalVolume` | `pvremove` |
| `ChangePhysicalVolume` | `pvchange` |
| `ResizePhysicalVolume` | `pvresize` |
| `DisplayPhysicalVolume` | `pvdisplay` |
| `ScanPhysicalVolumes` | `pvscan --cache` |
| `CheckPhysicalVolume` | `pvck` |
| `DumpPhysicalVolume` | `pvck --dump` |
| `CreateVolumeGroup` | `vgcreate` |
| `ListVolumeGroups` | `vgs` |
| `RemoveVolumeGroup` | `vgremove` |
| `ExtendVolumeGroup` | `vgextend` |
| `ReduceVolumeGroup` | `vgreduce` |
| `RenameVolumeGroup` | `vgrename` |
| `ChangeVolumeGroup` | `vgchange` |
| `ActivateVolumeGroup` | `vgchange -a y` |
| `DeactivateVolumeGroup` | `vgchange -a n` |
| `CheckVolumeGroup` | `vgck` |
| `DisplayVolumeGroup` | `vgdisplay` |
| `ScanVolumeGroups` | `vgscan` |
| `BackupVolumeGroupMetadata` | `vgcfgbackup` |
| `RestoreVolumeGroupMetadata` | `vgcfgrestore` |
| `ListVolumeGroupMetadataBackups` | `vgcfgrestore -l` |
| `ExportVolumeGroup` | `vgexport` |
| `ImportVolumeGroup` | `vgimport` |
| `MergeVolumeGroups` | `vgmerge` |
| `SplitVolumeGroup` | `vgsplit` |
| `MakeVolumeGroupNodes` | `vgmknodes` |
| `ImportClonedVolumeGroup` | `vgimportclone` |
| `ImportVolumeGroupDevices` | `vgimportdevices` |
| `CreateLogicalVolume` | `lvcreate` |
| `CreateThinPool` | `lvcreate --type thin-pool` |
| `CreateThinVolume` | `lvcreate --type thin` |
| `CreateSnapshot` | `lvcreate -s -L` |
| `CreateThinSnapshot` | `lvcreate -s` |
| `ListLogicalVolumes` | `lvs` |
| `RemoveLogicalVolume` | `lvremove` |
| `ChangeLogicalVolume` | `lvchange` |
| `ActivateLogicalVolume` | `lvchange -a y` |
| `DeactivateLogicalVolume` | `lvchange -a n` |
| `ExtendLogicalVolume` | `lvextend` |
| `ExtendLogicalVolumeByPolicy` | `lvextend --usepolicies` |
| `ReduceLogicalVolume` | `lvreduce` |
| `ResizeLogicalVolume` | `lvresize` |
| `RenameLogicalVolume` | `lvrename` |
| `DisplayLogicalVolume` | `lvdisplay` |
| `ScanLogicalVolumes` | `lvscan` |

Every operation takes an options struct. Its fields cover the command's
flags, and the embedded `CommonOptions` override the client environment
(device scoping, autobackup) for one call.

## Deliberately not wrapped

- `pvdata` and `vgconvert` operate on lvm1 metadata.
- `lvpoll` is an internal command of `lvmpolld`.
- Sparse volumes (`lvcreate -s --virtualsize`) are the predecessor of
  thin volumes.
- `--persistent`, `--major` and `--minor` set fixed device numbers and
  are deprecated.
- `--sysinit` and `--ignorelockingfailure` exist for early boot
  scripts.
- The udev forms of `pvscan`. Only `pvscan --cache` is wrapped, and
  plain listing is `ListPhysicalVolumes`' job.

Change operations address their targets through a selector:
`lvm.Device`, `lvm.Select` criteria (see lvmreport(7)), or `lvm.All`.
List operations filter with the `Select` options field instead:

```go
err := client.ChangePhysicalVolume(ctx, lvm.Select("pv_tags = {retiring}"),
	lvm.ChangePhysicalVolumeOptions{AddTags: []string{"drained"}})

pvs, err := client.ListPhysicalVolumes(ctx, lvm.ListPhysicalVolumesOptions{
	Select: "pv_free > 100g",
})
```

## Usage

```go
client := lvm.New()

err := client.CreatePhysicalVolume(ctx, "/dev/sdb", lvm.CreatePhysicalVolumeOptions{})
if err != nil {
	return err
}

err = client.ChangePhysicalVolume(ctx, lvm.Device("/dev/sdb"), lvm.ChangePhysicalVolumeOptions{
	AddTags:     []string{"ssd"},
	Allocatable: lvm.Bool(true),
})
if err != nil {
	return err
}

pvs, err := client.ListPhysicalVolumes(ctx, lvm.ListPhysicalVolumesOptions{})
if err != nil {
	return err
}

for _, pv := range pvs {
	fmt.Printf("%s: %d bytes free\n", pv.Device, pv.FreeBytes)
}
```

## Errors

Failed commands return an `*lvm.Error` with exit code and stderr. Known
failures also match `errors.Is` sentinels: `ErrNotFound`, `ErrAlreadyExists`, `ErrInUse`, `ErrNotAllowed`,
`ErrPermission`, `ErrInvalidCommand`.

## Notes

- A `Client` is safe for concurrent use. lvm itself serializes
  conflicting commands through its own locking, so parallel calls may
  wait on each other.
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
