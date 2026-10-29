// Package snapshot defines the on-disk snapshot framing shared by writers
// and readers. Task C04 adds the actual aggregate serialization.
package snapshot

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// Magic identifies a HapeServer snapshot.
var Magic = [8]byte{'H', 'A', 'P', 'E', 'S', 'N', 'P', '1'}

// Header is the fixed prefix of a snapshot file.
type Header struct {
	Version   uint16
	Shard     uint32
	Epoch     uint32
	WALOffset uint64
	Count     uint32
}

// ErrCorrupt is returned when a snapshot fails length or checksum checks.
var ErrCorrupt = errors.New("snapshot corrupt")

const headerSize = 8 + 2 + 4 + 4 + 8 + 4

// Encode frames header + payload + crc32c.
func Encode(h Header, payload []byte) []byte {
	out := make([]byte, headerSize+len(payload)+4)
	copy(out[0:8], Magic[:])
	binary.BigEndian.PutUint16(out[8:10], h.Version)
	binary.BigEndian.PutUint32(out[10:14], h.Shard)
	binary.BigEndian.PutUint32(out[14:18], h.Epoch)
	binary.BigEndian.PutUint64(out[18:26], h.WALOffset)
	binary.BigEndian.PutUint32(out[26:30], h.Count)
	copy(out[headerSize:], payload)
	sum := crc32.Checksum(out[:headerSize+len(payload)], crc32.MakeTable(crc32.Castagnoli))
	binary.BigEndian.PutUint32(out[headerSize+len(payload):], sum)
	return out
}

// Decode validates and splits a snapshot file.
func Decode(b []byte) (Header, []byte, error) {
	var h Header
	if len(b) < headerSize+4 {
		return h, nil, ErrCorrupt
	}
	if string(b[0:8]) != string(Magic[:]) {
		return h, nil, ErrCorrupt
	}
	h.Version = binary.BigEndian.Uint16(b[8:10])
	h.Shard = binary.BigEndian.Uint32(b[10:14])
	h.Epoch = binary.BigEndian.Uint32(b[14:18])
	h.WALOffset = binary.BigEndian.Uint64(b[18:26])
	h.Count = binary.BigEndian.Uint32(b[26:30])
	payload := b[headerSize : len(b)-4]
	want := binary.BigEndian.Uint32(b[len(b)-4:])
	got := crc32.Checksum(b[:len(b)-4], crc32.MakeTable(crc32.Castagnoli))
	if want != got {
		return h, nil, ErrCorrupt
	}
	return h, payload, nil
}
