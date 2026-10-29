// Package shard maps world entities onto a consistent-hash ring.
package shard

import "hash/crc32"

// OfPlayer returns the shard for a player aggregate.
func OfPlayer(worldID uint32, playerID uint64) uint32 {
	buf := make([]byte, 12)
	putUint32(buf[0:4], worldID)
	putUint64(buf[4:12], playerID)
	return crc32.ChecksumIEEE(buf)
}

// OfRegion returns the shard for a 64x64 map region.
func OfRegion(worldID uint32, rx, ry uint8) uint32 {
	buf := []byte{
		byte(worldID), byte(worldID >> 8), byte(worldID >> 16), byte(worldID >> 24),
		rx, ry,
	}
	return crc32.ChecksumIEEE(buf)
}

func putUint32(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}

func putUint64(b []byte, v uint64) {
	for i := 0; i < 8; i++ {
		b[i] = byte(v >> (8 * i))
	}
}
