package services

import (
	"encoding/binary"
	"net"
	"testing"
)

// buildMinimalMMDB builds a minimal valid MaxMind DB file (binary format
// v2.0) containing a single /32 record mapping ip to the country iso_code
// "US". It lets the routing tests exercise the local MaxMind path without
// shipping a real (multi-MB) database.
//
// Layout: 12-byte header, 24-bit-record search tree (6-byte nodes, 33 nodes
// for one /32), 16-byte separator, data section, metadata section.
func buildMinimalMMDB(t *testing.T, ip string) []byte {
	t.Helper()

	const (
		nodeSize  = 6       // 24-bit records: two 3-byte pointers
		nodeCount = 32      // one node per bit of the /32 prefix
		dataPtr   = 32 + 16 // first byte of the data section
	)

	ip4 := net.ParseIP(ip).To4()
	if ip4 == nil {
		t.Fatalf("buildMinimalMMDB: not an IPv4 address: %q", ip)
	}

	// Search tree. Each level follows one bit of the IP; the unused branch
	// points to "no data" (record value == nodeCount). The last node points
	// its matching branch at the data section.
	tree := make([]byte, nodeCount*nodeSize)
	for i := 0; i < 32; i++ {
		bit := (ip4[i/8] >> uint(7-i%8)) & 1
		next := i + 1
		if i == 31 {
			next = dataPtr
		}
		left, right := nodeCount, nodeCount
		if bit == 0 {
			left = next
		} else {
			right = next
		}
		put24(tree[i*nodeSize:], left)
		put24(tree[i*nodeSize+3:], right)
	}

	// Data section: map{ "country": map{ "iso_code": "US" } }. The top-level
	// record is a map (the outer mapField), whose single value is the
	// "country" sub-map.
	data := []byte{}
	data = append(data, mapField(1)...) // outer record map, 1 pair
	data = append(data, strField("country")...)
	data = append(data, mapField(1)...) // country map, 1 pair
	data = append(data, strField("iso_code")...)
	data = append(data, strField("US")...)

	// Metadata section: marker + map of the required fields.
	meta := []byte{0xab, 0xcd, 0xef, 'M', 'a', 'x', 'M', 'i', 'n', 'd', '.', 'c', 'o', 'm'}
	meta = append(meta, mapField(8)...)
	meta = append(meta, strField("node_count")...)
	meta = append(meta, 0xC1, byte(nodeCount)) // unsigned 32-bit int
	meta = append(meta, strField("record_size")...)
	meta = append(meta, 0xA1, 24) // unsigned 16-bit int
	meta = append(meta, strField("ip_version")...)
	meta = append(meta, 0xA1, 4) // unsigned 16-bit int
	meta = append(meta, strField("database_type")...)
	meta = append(meta, strField("GeoLite2-City")...)
	meta = append(meta, strField("binary_format_major_version")...)
	meta = append(meta, 0xA1, 2)
	meta = append(meta, strField("binary_format_minor_version")...)
	meta = append(meta, 0xA1, 0)
	meta = append(meta, strField("build_epoch")...)
	meta = append(meta, 0x08, 0x02) // extended type 2 = uint64 (type 9), 8-byte payload
	var epoch [8]byte
	binary.BigEndian.PutUint64(epoch[:], 0)
	meta = append(meta, epoch[:]...)
	meta = append(meta, strField("description")...)
	meta = append(meta, mapField(1)...)
	meta = append(meta, strField("en")...)
	meta = append(meta, strField("test")...)

	// No header: the search tree starts at byte 0 of the file (the reader
	// locates the metadata by its marker and assumes the tree is first).
	file := make([]byte, 0, len(tree)+16+len(data)+len(meta))
	file = append(file, tree...)
	file = append(file, make([]byte, 16)...)
	file = append(file, data...)
	file = append(file, meta...)
	return file
}

// put24 writes v as a 24-bit big-endian pointer.
func put24(buf []byte, v int) {
	buf[0] = byte(v >> 16)
	buf[1] = byte(v >> 8)
	buf[2] = byte(v)
}

// strField encodes a UTF-8 string data field (control byte + payload). It
// supports lengths below 29, the single-byte length range.
func strField(s string) []byte {
	return append([]byte{0x40 | byte(len(s))}, s...)
}

// mapField encodes a map data field header with n key/value pairs. The
// control byte carries the pair count (single-byte range, n < 29); the
// extended type byte is 0 because map is type 7 and extended types are
// stored as (type - 7).
func mapField(n int) []byte {
	return []byte{byte(n), 0x00}
}
