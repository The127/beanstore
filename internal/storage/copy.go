package storage

import (
	"context"
	"fmt"
	"os"
)

// CopyDevice copies every non-zero chunk from the source device to
// the target and syncs it. Untouched space on a fresh thin target
// reads as zeros.
func CopyDevice(ctx context.Context, sourcePath, targetPath string, progress func(uint64)) error {
	target, err := os.OpenFile(targetPath, os.O_WRONLY, 0) //nolint:gosec // the daemon's own device node
	if err != nil {
		return fmt.Errorf("opening copy target: %w", err)
	}
	defer func() {
		_ = target.Close()
	}()

	_, _, err = ReadDevice(ctx, sourcePath, func(frame Frame) error {
		_, err := target.WriteAt(frame.Data, int64(frame.Offset)) //nolint:gosec // the reader bounds offsets to the device size
		if err != nil {
			return fmt.Errorf("writing copy at %d: %w", frame.Offset, err)
		}
		progress(frame.Offset + uint64(len(frame.Data)))

		return nil
	})
	if err != nil {
		return err
	}

	err = target.Sync()
	if err != nil {
		return fmt.Errorf("syncing copy target: %w", err)
	}

	return nil
}
