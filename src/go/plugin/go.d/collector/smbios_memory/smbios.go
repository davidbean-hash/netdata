// SPDX-License-Identifier: GPL-3.0-or-later

package smbios_memory

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
)

// SMBIOS structure types we care about (SMBIOS spec, section 6.1.2).
const (
	smbiosTypeMemoryDevice = 17
	smbiosTypeEndOfTable   = 127

	// Every structure starts with a 4-byte header: type(1), length(1), handle(2).
	smbiosHeaderLen = 4
)

// Field offsets within a Memory Device (type 17) structure, counted from the
// start of the structure (offset 0 == type byte). See SMBIOS spec section 7.18.
const (
	off17Size            = 0x0C // WORD
	off17FormFactor      = 0x0E // BYTE (enum)
	off17DeviceLocator   = 0x10 // BYTE (string index)
	off17BankLocator     = 0x11 // BYTE (string index)
	off17MemoryType      = 0x12 // BYTE (enum)
	off17Speed           = 0x15 // WORD, MT/s (SMBIOS 2.3+)
	off17Manufacturer    = 0x17 // BYTE (string index) (SMBIOS 2.3+)
	off17PartNumber      = 0x1A // BYTE (string index) (SMBIOS 2.3+)
	off17Attributes      = 0x1B // BYTE, rank in bits 3:0 (SMBIOS 2.6+)
	off17ExtendedSize    = 0x1C // DWORD, MB (SMBIOS 2.7+)
	off17ConfiguredSpeed = 0x20 // WORD, MT/s (SMBIOS 2.7+)
)

// smbiosStructure is a single decoded SMBIOS structure. data holds the
// formatted area (including the 4-byte header) so that spec field offsets can
// be used directly. strings holds the structure's string-set.
type smbiosStructure struct {
	stype   uint8
	length  uint8
	handle  uint16
	data    []byte
	strings []string
}

// parseSMBIOSStructures walks a raw SMBIOS structure table (the exact contents
// of /sys/firmware/dmi/tables/DMI) and returns the decoded structures.
//
// Each structure consists of a formatted area of length bytes followed by a
// string-set: a run of NUL-terminated strings terminated by an additional NUL
// (so the whole set ends in a double NUL). A structure with no strings is
// therefore terminated by two NUL bytes.
func parseSMBIOSStructures(b []byte) ([]smbiosStructure, error) {
	var out []smbiosStructure

	for i := 0; i < len(b); {
		if i+smbiosHeaderLen > len(b) {
			return nil, fmt.Errorf("truncated structure header at offset %d", i)
		}

		stype := b[i]
		length := int(b[i+1])
		if length < smbiosHeaderLen {
			return nil, fmt.Errorf("invalid structure length %d at offset %d", length, i)
		}
		if i+length > len(b) {
			return nil, fmt.Errorf("formatted area (len %d) overruns table at offset %d", length, i)
		}

		handle := binary.LittleEndian.Uint16(b[i+2:])
		data := b[i : i+length]

		// Locate the terminating double-NUL that ends the string-set.
		strStart := i + length
		end := strStart
		for {
			if end+1 >= len(b) {
				return nil, fmt.Errorf("unterminated string-set for structure type %d handle %#x", stype, handle)
			}
			if b[end] == 0 && b[end+1] == 0 {
				break
			}
			end++
		}

		var strs []string
		if region := b[strStart:end]; len(region) > 0 {
			for _, part := range bytes.Split(region, []byte{0}) {
				strs = append(strs, string(part))
			}
		}

		out = append(out, smbiosStructure{
			stype:   stype,
			length:  uint8(length),
			handle:  handle,
			data:    data,
			strings: strs,
		})

		// Advance past the formatted area and the terminating double-NUL.
		i = end + 2

		if stype == smbiosTypeEndOfTable {
			break
		}
	}

	return out, nil
}

func (s smbiosStructure) u8(off int) (uint8, bool) {
	if off < 0 || off >= int(s.length) {
		return 0, false
	}
	return s.data[off], true
}

func (s smbiosStructure) u16(off int) (uint16, bool) {
	if off < 0 || off+2 > int(s.length) {
		return 0, false
	}
	return binary.LittleEndian.Uint16(s.data[off:]), true
}

func (s smbiosStructure) u32(off int) (uint32, bool) {
	if off < 0 || off+4 > int(s.length) {
		return 0, false
	}
	return binary.LittleEndian.Uint32(s.data[off:]), true
}

// str resolves a string-index field at the given offset. SMBIOS string indexes
// are 1-based; index 0 means "not specified".
func (s smbiosStructure) str(off int) string {
	idx, ok := s.u8(off)
	if !ok || idx == 0 || int(idx) > len(s.strings) {
		return ""
	}
	return strings.TrimSpace(s.strings[idx-1])
}
