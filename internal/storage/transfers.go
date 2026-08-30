package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/The127/beanstore/internal/config"
	"github.com/The127/beanstore/internal/logging"
	"github.com/The127/beanstore/lvm"
)

// transferTagPrefix persists a transfer's id on its incoming lv.
const transferTagPrefix = "beanstore.transfer="

// Transfer errors.
var (
	ErrTransferLimit   = errors.New("inbound transfer limit reached")
	ErrTransferUsed    = errors.New("transfer id already used")
	ErrTransferUnknown = errors.New("unknown transfer")
	ErrTransferBusy    = errors.New("transfer stream already active")
	ErrBadFrame        = errors.New("invalid frame")
	ErrDigestMismatch  = errors.New("digest mismatch")
)

// Transfers tracks the node's live inbound transfers. Sessions are
// volatile, a restart forgets them and recovery removes their
// volumes.
type Transfers struct {
	client *lvm.Client
	cfg    config.Config
	// background outlives streams, grace expiry cleans up on it.
	background context.Context

	mu       sync.Mutex
	sessions map[string]*transferSession
	dead     map[string]bool
}

type transferSession struct {
	volumeID   string
	path       string
	sizeBytes  uint64
	device     *os.File
	digest     *DigestBuilder
	nextOffset uint64
	streaming  bool
	terminal   bool
	graceTimer *time.Timer
}

// NewTransfers returns an empty transfer table.
func NewTransfers(ctx context.Context, client *lvm.Client, cfg config.Config) *Transfers {
	return &Transfers{
		client:     client,
		cfg:        cfg,
		background: ctx,
		sessions:   map[string]*transferSession{},
		dead:       map[string]bool{},
	}
}

// PrepareReceive creates the transfer's INCOMING volume and registers
// the session. The grace timer runs until a stream attaches.
func (t *Transfers) PrepareReceive(ctx context.Context, transferID, volumeID string, sizeBytes uint64) error {
	t.mu.Lock()
	if t.dead[transferID] || t.sessions[transferID] != nil {
		t.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrTransferUsed, transferID)
	}

	live := 0
	for _, session := range t.sessions {
		if !session.terminal {
			live++
		}
	}
	if live >= t.cfg.MaxInboundTransfers {
		t.mu.Unlock()
		return fmt.Errorf("%w: %d", ErrTransferLimit, live)
	}

	// reserve the id before the lvm calls, a failed create consumes it
	session := &transferSession{volumeID: volumeID, sizeBytes: sizeBytes, digest: NewDigestBuilder()}
	t.sessions[transferID] = session
	t.mu.Unlock()

	err := t.client.CreateThinVolume(ctx, t.cfg.VolumeGroup, t.cfg.ThinPool, volumeID, sizeBytes,
		lvm.CreateThinVolumeOptions{
			AddTags: []string{StateTag(StateIncoming), transferTagPrefix + transferID},
		})
	if err != nil {
		t.finish(transferID)
		return fmt.Errorf("creating incoming volume %s: %w", volumeID, err)
	}

	volume, err := GetVolume(ctx, t.client, t.cfg, volumeID)
	if err != nil {
		t.destroy(transferID)
		return err
	}

	t.mu.Lock()
	session.path = volume.Path
	t.armGrace(transferID, session)
	t.mu.Unlock()

	return nil
}

// NextOffset answers where the transfer's stream continues.
func (t *Transfers) NextOffset(transferID string) (uint64, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	session := t.sessions[transferID]
	if session == nil || session.terminal {
		return 0, fmt.Errorf("%w: %s", ErrTransferUnknown, transferID)
	}

	return session.nextOffset, nil
}

// Attach claims the transfer's stream slot and stops the grace timer.
func (t *Transfers) Attach(transferID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	session := t.sessions[transferID]
	if session == nil || session.terminal {
		return fmt.Errorf("%w: %s", ErrTransferUnknown, transferID)
	}
	if session.streaming {
		return fmt.Errorf("%w: %s", ErrTransferBusy, transferID)
	}

	session.streaming = true
	if session.graceTimer != nil {
		session.graceTimer.Stop()
		session.graceTimer = nil
	}

	return nil
}

// Detach releases the stream slot and re-arms the grace timer.
func (t *Transfers) Detach(transferID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	session := t.sessions[transferID]
	if session == nil || session.terminal {
		return
	}

	session.streaming = false
	t.armGrace(transferID, session)
}

// Write validates and applies one frame. Skipped ranges before the
// frame enter the digest as zero chunks.
func (t *Transfers) Write(transferID string, offset uint64, data []byte) error {
	t.mu.Lock()
	session := t.sessions[transferID]
	t.mu.Unlock()
	if session == nil {
		return fmt.Errorf("%w: %s", ErrTransferUnknown, transferID)
	}

	switch {
	case offset%TransferChunkBytes != 0:
		return fmt.Errorf("%w: offset %d is not chunk aligned", ErrBadFrame, offset)

	case offset < session.nextOffset:
		return fmt.Errorf("%w: offset %d before %d", ErrBadFrame, offset, session.nextOffset)

	case offset+uint64(len(data)) > session.sizeBytes:
		return fmt.Errorf("%w: offset %d length %d beyond size %d", ErrBadFrame, offset, len(data), session.sizeBytes)

	case len(data) == 0 || len(data) > TransferChunkBytes:
		return fmt.Errorf("%w: length %d", ErrBadFrame, len(data))

	case uint64(len(data)) < TransferChunkBytes && offset+uint64(len(data)) != session.sizeBytes:
		return fmt.Errorf("%w: short frame at %d is not final", ErrBadFrame, offset)
	}

	if session.device == nil {
		device, err := os.OpenFile(session.path, os.O_WRONLY, 0)
		if err != nil {
			return fmt.Errorf("opening incoming volume: %w", err)
		}
		session.device = device
	}

	for gap := session.nextOffset; gap < offset; gap += TransferChunkBytes {
		session.digest.AddZeroChunk(min(TransferChunkBytes, session.sizeBytes-gap))
	}
	session.digest.AddChunk(data)

	_, err := session.device.WriteAt(data, int64(offset)) //nolint:gosec // offset is bounds checked above
	if err != nil {
		return fmt.Errorf("writing incoming volume at %d: %w", offset, err)
	}
	session.nextOffset = offset + uint64(len(data))

	return nil
}

// Commit verifies the digest, makes the volume durable and retags it
// READY. The transfer ends either way, a mismatch destroys it.
func (t *Transfers) Commit(ctx context.Context, transferID string, digest []byte) error {
	t.mu.Lock()
	session := t.sessions[transferID]
	if session == nil || session.terminal || session.streaming {
		t.mu.Unlock()
		if session != nil && session.streaming {
			return fmt.Errorf("%w: %s", ErrTransferBusy, transferID)
		}
		return fmt.Errorf("%w: %s", ErrTransferUnknown, transferID)
	}
	t.mu.Unlock()

	for gap := session.nextOffset; gap < session.sizeBytes; gap += TransferChunkBytes {
		session.digest.AddZeroChunk(min(TransferChunkBytes, session.sizeBytes-gap))
	}
	if !bytes.Equal(session.digest.Sum(session.sizeBytes), digest) {
		t.destroy(transferID)
		return fmt.Errorf("%w: %s", ErrDigestMismatch, transferID)
	}

	if session.device != nil {
		err := session.device.Sync()
		if err != nil {
			t.destroy(transferID)
			return fmt.Errorf("syncing incoming volume: %w", err)
		}
		// deactivation refuses while the device is open
		_ = session.device.Close()
		session.device = nil
	}

	err := t.client.DeactivateLogicalVolume(ctx, lvm.Name(t.cfg.VolumeGroup+"/"+session.volumeID),
		lvm.DeactivateLogicalVolumeOptions{})
	if err != nil {
		t.destroy(transferID)
		return fmt.Errorf("deactivating incoming volume: %w", err)
	}

	err = t.client.ChangeLogicalVolume(ctx, lvm.Name(t.cfg.VolumeGroup+"/"+session.volumeID),
		lvm.ChangeLogicalVolumeOptions{
			AddTags:    []string{StateTag(StateReady)},
			RemoveTags: []string{StateTag(StateIncoming), transferTagPrefix + transferID},
		})
	if err != nil {
		t.destroy(transferID)
		return fmt.Errorf("readying received volume: %w", err)
	}

	t.finish(transferID)

	return nil
}

// Abort destroys the transfer and its volume. Aborting an unknown or
// finished transfer succeeds.
func (t *Transfers) Abort(transferID string) {
	t.mu.Lock()
	session := t.sessions[transferID]
	t.mu.Unlock()
	if session == nil || session.terminal {
		return
	}

	t.destroy(transferID)
}

// armGrace schedules the transfer's destruction. Callers hold the
// lock.
func (t *Transfers) armGrace(transferID string, session *transferSession) {
	session.graceTimer = time.AfterFunc(t.cfg.TransferGrace, func() {
		logging.FromContext(t.background).Info("transfer grace expired", "transfer", transferID)
		t.destroy(transferID)
	})
}

// finish marks the session terminal without touching the volume.
func (t *Transfers) finish(transferID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	session := t.sessions[transferID]
	if session == nil {
		return
	}

	session.terminal = true
	if session.graceTimer != nil {
		session.graceTimer.Stop()
	}
	if session.device != nil {
		_ = session.device.Close()
		session.device = nil
	}
	delete(t.sessions, transferID)
	t.dead[transferID] = true
}

// destroy ends the session and removes its volume.
func (t *Transfers) destroy(transferID string) {
	t.mu.Lock()
	session := t.sessions[transferID]
	if session == nil || session.terminal {
		t.mu.Unlock()
		return
	}
	volumeID := session.volumeID
	t.mu.Unlock()

	t.finish(transferID)

	err := RemoveVolume(t.background, t.client, t.cfg, volumeID)
	if err != nil {
		logging.FromContext(t.background).Error("removing incoming volume",
			"transfer", transferID, "volume", volumeID, "error", err)
	}
}
