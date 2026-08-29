package storage

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"sync"
)

// TransferChunkBytes is the transfer frame and digest chunk size.
const TransferChunkBytes = 1 << 20

// zeroChunkHash is the sha256 of one full chunk of zeros.
var zeroChunkHash = sync.OnceValue(func() []byte {
	digest := sha256.Sum256(make([]byte, TransferChunkBytes))
	return digest[:]
})

// DigestBuilder computes the content digest of a volume: the sha256
// over the ordered per chunk sha256 hashes plus the volume size.
// Chunks must be added in order, zero chunks included. The digest is
// independent of which chunks a sender chose to transmit.
type DigestBuilder struct {
	outer hash.Hash
}

// NewDigestBuilder returns a builder with no chunks added.
func NewDigestBuilder() *DigestBuilder {
	return &DigestBuilder{outer: sha256.New()}
}

// AddChunk hashes the next chunk's bytes.
func (b *DigestBuilder) AddChunk(data []byte) {
	digest := sha256.Sum256(data)
	b.outer.Write(digest[:])
}

// AddZeroChunk hashes a skipped all-zero chunk of the given length.
func (b *DigestBuilder) AddZeroChunk(length uint64) {
	if length >= TransferChunkBytes {
		b.outer.Write(zeroChunkHash())
		return
	}

	digest := sha256.Sum256(make([]byte, length))
	b.outer.Write(digest[:])
}

// Sum finalizes the digest over the added chunks and the volume size.
func (b *DigestBuilder) Sum(sizeBytes uint64) []byte {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], sizeBytes)
	b.outer.Write(size[:])

	return b.outer.Sum(nil)
}
