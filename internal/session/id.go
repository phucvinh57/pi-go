package session

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"time"
)

// newSessionID returns a UUIDv7: 48 bits of Unix milliseconds, so IDs sort by
// start time, then random bits.
func newSessionID(now time.Time) string {
	var b [16]byte
	// The milliseconds go in the first 6 bytes (the top 48 bits of the first
	// word); the random part fills the rest.
	binary.BigEndian.PutUint64(b[:8], uint64(now.UnixMilli())<<16)
	_, _ = rand.Read(b[6:])
	b[6] = b[6]&0x0f | 0x70 // version 7
	b[8] = b[8]&0x3f | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// newEntryID returns a short random ID for an entry.
func newEntryID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
