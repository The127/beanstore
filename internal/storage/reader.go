package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
)

// Frame is one chunk of volume content at its byte offset.
type Frame struct {
	Offset uint64
	Data   []byte
}

var zeroChunk = make([]byte, TransferChunkBytes)

// ReadDevice reads the device sequentially in chunks, streams every
// non-zero chunk to the sink and returns the device size and content
// digest. Skipped zero chunks are part of the digest. The sink must
// not retain the frame's data across calls.
func ReadDevice(ctx context.Context, path string, sink func(Frame) error) (uint64, []byte, error) {
	device, err := os.Open(path) //nolint:gosec // the daemon's own device node
	if err != nil {
		return 0, nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer func() {
		_ = device.Close()
	}()

	end, err := device.Seek(0, io.SeekEnd)
	if err != nil || end < 0 {
		return 0, nil, fmt.Errorf("sizing %s: %w", path, err)
	}
	size := uint64(end)
	_, err = device.Seek(0, io.SeekStart)
	if err != nil {
		return 0, nil, fmt.Errorf("rewinding %s: %w", path, err)
	}

	builder := NewDigestBuilder()
	buffer := make([]byte, TransferChunkBytes)
	for offset := uint64(0); offset < size; offset += TransferChunkBytes {
		err = ctx.Err()
		if err != nil {
			return 0, nil, err
		}

		chunk := buffer
		if remaining := size - offset; remaining < TransferChunkBytes {
			chunk = buffer[:remaining]
		}
		_, err = io.ReadFull(device, chunk)
		if err != nil {
			return 0, nil, fmt.Errorf("reading %s at %d: %w", path, offset, err)
		}

		if bytes.Equal(chunk, zeroChunk[:len(chunk)]) {
			builder.AddZeroChunk(uint64(len(chunk)))
			continue
		}

		builder.AddChunk(chunk)
		err = sink(Frame{Offset: offset, Data: chunk})
		if err != nil {
			return 0, nil, err
		}
	}

	return size, builder.Sum(size), nil
}
