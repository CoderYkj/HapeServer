// Package snapshot defines the on-disk snapshot framing shared by writers
// and readers. Task C04 adds the actual aggregate serialization.
package snapshot

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
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
	var buf bytes.Buffer
	buf.Grow(headerSize + len(payload) + 4)
	_ = EncodeTo(&buf, h, payload)
	return buf.Bytes()
}

// EncodeTo writes the same frame without allocating the whole snapshot.
func EncodeTo(w io.Writer, h Header, payload []byte) error {
	var hdr [headerSize]byte
	copy(hdr[0:8], Magic[:])
	binary.BigEndian.PutUint16(hdr[8:10], h.Version)
	binary.BigEndian.PutUint32(hdr[10:14], h.Shard)
	binary.BigEndian.PutUint32(hdr[14:18], h.Epoch)
	binary.BigEndian.PutUint64(hdr[18:26], h.WALOffset)
	binary.BigEndian.PutUint32(hdr[26:30], h.Count)
	hash := crc32.New(crc32.MakeTable(crc32.Castagnoli))
	if _, err := hash.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := hash.Write(payload); err != nil {
		return err
	}
	var sum [4]byte
	binary.BigEndian.PutUint32(sum[:], hash.Sum32())
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	_, err := w.Write(sum[:])
	return err
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
