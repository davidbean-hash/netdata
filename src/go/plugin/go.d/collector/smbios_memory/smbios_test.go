// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package smbios_memory

import (
	"encoding/binary"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	gib = uint64(1024 * 1024 * 1024)
)

// buildStructure assembles one raw SMBIOS structure: header + formatted area +
// string-set (double-NUL terminated).
func buildStructure(stype uint8, handle uint16, formatted []byte, strs []string) []byte {
	length := smbiosHeaderLen + len(formatted)
	out := make([]byte, 0, length+16)
	out = append(out, stype, byte(length))
	out = binary.LittleEndian.AppendUint16(out, handle)
	out = append(out, formatted...)
	if len(strs) == 0 {
		out = append(out, 0, 0)
		return out
	}
	for _, s := range strs {
		out = append(out, []byte(s)...)
		out = append(out, 0)
	}
	out = append(out, 0)
	return out
}

func TestParseSMBIOSStructures_realTableLayout(t *testing.T) {
	// Two structures with strings + an end-of-table marker, exercising the
	// header, formatted area, and double-NUL string-set termination.
	var b []byte
	b = append(b, buildStructure(1, 0x0001, make([]byte, 23), []string{"Vendor", "Model"})...)
	b = append(b, buildStructure(4, 0x0002, make([]byte, 10), nil)...)
	b = append(b, buildStructure(smbiosTypeEndOfTable, 0x0003, nil, nil)...)

	got, err := parseSMBIOSStructures(b)
	require.NoError(t, err)
	require.Len(t, got, 3)

	assert.Equal(t, uint8(1), got[0].stype)
	assert.Equal(t, uint16(0x0001), got[0].handle)
	assert.Equal(t, []string{"Vendor", "Model"}, got[0].strings)

	assert.Equal(t, uint8(4), got[1].stype)
	assert.Empty(t, got[1].strings)

	assert.Equal(t, uint8(smbiosTypeEndOfTable), got[2].stype)
}

func TestParseSMBIOSStructures_errors(t *testing.T) {
	tests := map[string][]byte{
		"truncated header":        {17, 40},
		"length below header":     {17, 2, 0, 0},
		"formatted area overruns": {17, 40, 0, 0, 1, 2, 3},
		"unterminated string set": append([]byte{1, 6, 0, 0, 0, 0}, []byte("abc")...),
	}
	for name, b := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := parseSMBIOSStructures(b)
			assert.Error(t, err)
		})
	}
}

func TestParseMemoryDevice_fixture(t *testing.T) {
	b, err := os.ReadFile("testdata/type17.bin")
	require.NoError(t, err)

	structs, err := parseSMBIOSStructures(b)
	require.NoError(t, err)

	var devices []memoryDevice
	for _, s := range structs {
		if s.stype == smbiosTypeMemoryDevice {
			devices = append(devices, parseMemoryDevice(s))
		}
	}
	require.Len(t, devices, 4)

	// DIMM_A1: 16 GiB DDR4 SODIMM, dual rank, configured speed preferred (2933).
	a1 := devices[0]
	assert.True(t, a1.present)
	assert.Equal(t, "DIMM_A1", a1.locator)
	assert.Equal(t, "BANK 0", a1.bankLocator)
	assert.Equal(t, 16*gib, a1.sizeBytes)
	assert.Equal(t, "DDR4", a1.memoryType)
	assert.Equal(t, "SODIMM", a1.formFactor)
	assert.Equal(t, uint64(2933), a1.speedMTs)
	assert.Equal(t, "Samsung", a1.manufacturer)
	assert.Equal(t, "M471A2K43DB1-CWE", a1.partNumber)
	assert.Equal(t, uint8(2), a1.ranks)

	// DIMM_A2: empty slot, no module fields.
	a2 := devices[1]
	assert.False(t, a2.present)
	assert.Equal(t, "DIMM_A2", a2.locator)
	assert.Equal(t, uint64(0), a2.sizeBytes)
	assert.Empty(t, a2.manufacturer)
	assert.Empty(t, a2.partNumber)

	// DIMM_B1: 32 GiB DDR5 DIMM via the Extended Size field.
	b1 := devices[2]
	assert.True(t, b1.present)
	assert.Equal(t, "DIMM_B1", b1.locator)
	assert.Equal(t, 32*gib, b1.sizeBytes)
	assert.Equal(t, "DDR5", b1.memoryType)
	assert.Equal(t, "DIMM", b1.formFactor)
	assert.Equal(t, uint64(4800), b1.speedMTs)
	assert.Equal(t, "Micron", b1.manufacturer)
	assert.Equal(t, uint8(1), b1.ranks)

	// DIMM_C1: 8 GiB DDR3, short (SMBIOS 2.3) 27-byte structure: no attributes
	// or configured-speed fields, so ranks are unknown and max speed is used.
	c1 := devices[3]
	assert.True(t, c1.present)
	assert.Equal(t, "DIMM_C1", c1.locator)
	assert.Equal(t, 8*gib, c1.sizeBytes)
	assert.Equal(t, "DDR3", c1.memoryType)
	assert.Equal(t, uint64(1600), c1.speedMTs)
	assert.Equal(t, "Hynix", c1.manufacturer)
	assert.Equal(t, uint8(0), c1.ranks)
}

func TestMemoryDeviceSize(t *testing.T) {
	// helper to build a minimal type 17 structure with a given size word and
	// optional extended size (needs a length that reaches offset 0x20).
	build := func(sizeWord uint16, extSize uint32) smbiosStructure {
		f := make([]byte, 0x24-smbiosHeaderLen)
		binary.LittleEndian.PutUint16(f[off17Size-smbiosHeaderLen:], sizeWord)
		binary.LittleEndian.PutUint32(f[off17ExtendedSize-smbiosHeaderLen:], extSize)
		raw := buildStructure(smbiosTypeMemoryDevice, 1, f, nil)
		got, err := parseSMBIOSStructures(raw)
		require.NoError(t, err)
		return got[0]
	}

	tests := map[string]struct {
		sizeWord  uint16
		extSize   uint32
		wantBytes uint64
	}{
		"no module installed": {sizeWord: 0x0000, wantBytes: 0},
		"unknown size":        {sizeWord: 0xFFFF, wantBytes: 0},
		"megabytes":           {sizeWord: 16384, wantBytes: 16 * gib},
		"kilobytes":           {sizeWord: 0x8000 | 512, wantBytes: 512 * 1024},
		"extended size":       {sizeWord: 0x7FFF, extSize: 65536, wantBytes: 64 * gib},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			s := build(test.sizeWord, test.extSize)
			assert.Equal(t, test.wantBytes, memoryDeviceSize(test.sizeWord, s))
		})
	}
}

func TestParseMemoryDevicePresence(t *testing.T) {
	build := func(sizeWord uint16) smbiosStructure {
		f := make([]byte, 0x24-smbiosHeaderLen)
		binary.LittleEndian.PutUint16(f[off17Size-smbiosHeaderLen:], sizeWord)
		raw := buildStructure(smbiosTypeMemoryDevice, 1, f, nil)
		got, err := parseSMBIOSStructures(raw)
		require.NoError(t, err)
		return got[0]
	}

	// Only size 0x0000 means the slot is empty.
	empty := parseMemoryDevice(build(0x0000))
	assert.False(t, empty.present)
	assert.Equal(t, uint64(0), empty.sizeBytes)

	// 0xFFFF means an installed module of unknown size: still present, size 0.
	unknown := parseMemoryDevice(build(0xFFFF))
	assert.True(t, unknown.present)
	assert.Equal(t, uint64(0), unknown.sizeBytes)
}

func TestDimmLocation(t *testing.T) {
	c := &Collector{}

	// Unique locator: used as-is.
	assert.Equal(t, "DIMM_A1", c.dimmLocation(memoryDevice{locator: "DIMM_A1", handle: 0x10}, false))
	// Missing locator: falls back to the handle.
	assert.Equal(t, "handle_0x0011", c.dimmLocation(memoryDevice{handle: 0x11}, false))
	// Duplicate locator: disambiguated with the handle.
	assert.Equal(t, "DIMM 0_0x0012", c.dimmLocation(memoryDevice{locator: "DIMM 0", handle: 0x12}, true))
}

func TestMemoryTypeAndFormFactorDecoding(t *testing.T) {
	assert.Equal(t, "DDR3", memoryType(0x18))
	assert.Equal(t, "DDR4", memoryType(0x1A))
	assert.Equal(t, "DDR5", memoryType(0x22))
	assert.Equal(t, "LPDDR5", memoryType(0x23))
	assert.Equal(t, "Unknown", memoryType(0x00))
	assert.Equal(t, "Unknown", memoryType(0xFE))

	assert.Equal(t, "DIMM", memoryFormFactor(0x09))
	assert.Equal(t, "SODIMM", memoryFormFactor(0x0D))
	assert.Equal(t, "Unknown", memoryFormFactor(0xFE))
}

func TestSMBIOSStringResolution(t *testing.T) {
	// A structure declaring string indexes, including an out-of-range index
	// and index 0 (not specified).
	f := make([]byte, 0x1B-smbiosHeaderLen)
	f[off17DeviceLocator-smbiosHeaderLen] = 1 // valid -> "  DIMM_A1  " (trimmed)
	f[off17BankLocator-smbiosHeaderLen] = 0   // not specified
	f[off17Manufacturer-smbiosHeaderLen] = 9  // out of range
	raw := buildStructure(smbiosTypeMemoryDevice, 1, f, []string{"  DIMM_A1  ", "BANK 0"})

	got, err := parseSMBIOSStructures(raw)
	require.NoError(t, err)

	s := got[0]
	assert.Equal(t, "DIMM_A1", s.str(off17DeviceLocator))
	assert.Empty(t, s.str(off17BankLocator))
	assert.Empty(t, s.str(off17Manufacturer))
}
