package storage

import (
	"context"
	"fmt"
	"slices"

	"github.com/The127/beanstore/internal/config"
	"github.com/The127/beanstore/internal/logging"
	"github.com/The127/beanstore/lvm"
)

// Setup verifies the configured vg and thin pool at startup. A missing
// pool is created when the config allows it, a missing vg is always
// fatal because creating one consumes raw disks.
func Setup(ctx context.Context, client *lvm.Client, cfg config.Config) error {
	vgs, err := client.ListVolumeGroups(ctx, lvm.ListVolumeGroupsOptions{
		Select: lvm.Select("vg_name = " + cfg.VolumeGroup),
	})
	if err != nil {
		return fmt.Errorf("looking up volume group %s: %w", cfg.VolumeGroup, err)
	}
	if len(vgs) == 0 {
		return fmt.Errorf("volume group %s does not exist, create it with vgcreate", cfg.VolumeGroup)
	}

	pools, err := client.ListLogicalVolumes(ctx, lvm.ListLogicalVolumesOptions{
		VG:     cfg.VolumeGroup,
		Select: lvm.Select("lv_name = " + cfg.ThinPool),
	})
	if err != nil {
		return fmt.Errorf("looking up thin pool %s/%s: %w", cfg.VolumeGroup, cfg.ThinPool, err)
	}

	if len(pools) > 0 {
		if !slices.Contains(pools[0].Layout, "pool") {
			return fmt.Errorf("%s/%s exists but is not a thin pool", cfg.VolumeGroup, cfg.ThinPool)
		}

		return checkChunkSize(pools[0])
	}

	if !cfg.CreatePool {
		return fmt.Errorf("thin pool %s/%s does not exist, create it or set create_pool", cfg.VolumeGroup, cfg.ThinPool)
	}

	size := cfg.PoolSize.Bytes
	if cfg.PoolSize.Percent > 0 {
		size = vgs[0].FreeBytes / 100 * cfg.PoolSize.Percent
	}

	err = client.CreateThinPool(ctx, cfg.VolumeGroup, cfg.ThinPool, size, lvm.CreateThinPoolOptions{})
	if err != nil {
		return fmt.Errorf("creating thin pool %s/%s: %w", cfg.VolumeGroup, cfg.ThinPool, err)
	}

	logging.FromContext(ctx).Info("created thin pool",
		"vg", cfg.VolumeGroup, "pool", cfg.ThinPool, "bytes", size)

	pools, err = client.ListLogicalVolumes(ctx, lvm.ListLogicalVolumesOptions{
		VG:     cfg.VolumeGroup,
		Select: lvm.Select("lv_name = " + cfg.ThinPool),
	})
	if err != nil || len(pools) == 0 {
		return fmt.Errorf("reading created pool %s/%s: %w", cfg.VolumeGroup, cfg.ThinPool, err)
	}

	return checkChunkSize(pools[0])
}

// checkChunkSize refuses pools whose chunk size does not divide the
// transfer chunk. A transfer frame partially covering a pool chunk
// would otherwise expose stale pool data on pools without zeroing.
// The refusal is conservative, it also covers pools with zeroing on.
func checkChunkSize(pool lvm.LogicalVolume) error {
	if pool.ChunkSizeBytes == 0 || TransferChunkBytes%pool.ChunkSizeBytes != 0 {
		return fmt.Errorf("pool chunk size %d does not divide the %d byte transfer chunk, transfers would be unsafe",
			pool.ChunkSizeBytes, TransferChunkBytes)
	}

	return nil
}
