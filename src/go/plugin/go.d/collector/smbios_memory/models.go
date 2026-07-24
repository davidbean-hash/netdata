// SPDX-License-Identifier: GPL-3.0-or-later

package smbios_memory

// memoryDevice is the inventory extracted from a single SMBIOS Memory Device
// (type 17) structure.
type memoryDevice struct {
	handle       uint16
	present      bool // a module is installed in this slot
	sizeBytes    uint64
	locator      string
	bankLocator  string
	memoryType   string // decoded, e.g. "DDR4", "DDR5"
	formFactor   string // decoded, e.g. "DIMM", "SODIMM"
	speedMTs     uint64 // configured speed if known, otherwise max speed
	manufacturer string
	partNumber   string
	ranks        uint8 // 0 == unknown
}

// parseMemoryDevice decodes a type 17 structure into a memoryDevice. All field
// reads are bounds-checked against the structure length, so structures shorter
// than a given SMBIOS revision simply leave the newer fields unset.
func parseMemoryDevice(s smbiosStructure) memoryDevice {
	d := memoryDevice{
		handle:       s.handle,
		locator:      s.str(off17DeviceLocator),
		bankLocator:  s.str(off17BankLocator),
		manufacturer: s.str(off17Manufacturer),
		partNumber:   s.str(off17PartNumber),
	}

	if v, ok := s.u8(off17FormFactor); ok {
		d.formFactor = memoryFormFactor(v)
	}
	if v, ok := s.u8(off17MemoryType); ok {
		d.memoryType = memoryType(v)
	}
	if raw, ok := s.u16(off17Size); ok {
		d.sizeBytes, d.present = memoryDeviceSize(raw, s)
	}
	d.speedMTs = memoryDeviceSpeed(s)
	if attr, ok := s.u8(off17Attributes); ok {
		d.ranks = attr & 0x0F
	}

	return d
}

// memoryDeviceSize decodes the Size field (offset 0x0C) into bytes. It reports
// whether a module is actually installed.
//
// Encoding (SMBIOS spec 7.18.5):
//   - 0x0000: no memory device installed in this slot
//   - 0xFFFF: size unknown
//   - 0x7FFF: size is in the Extended Size field (offset 0x1C), in MB
//   - otherwise: bit 15 selects the unit (0 => MB, 1 => KB), bits 14:0 the value
func memoryDeviceSize(raw uint16, s smbiosStructure) (uint64, bool) {
	switch raw {
	case 0x0000, 0xFFFF:
		return 0, false
	case 0x7FFF:
		ext, ok := s.u32(off17ExtendedSize)
		if !ok {
			return 0, false
		}
		mb := uint64(ext & 0x7FFFFFFF)
		return mb * 1024 * 1024, mb > 0
	default:
		if raw&0x8000 != 0 {
			return uint64(raw&0x7FFF) * 1024, true
		}
		return uint64(raw) * 1024 * 1024, true
	}
}

// memoryDeviceSpeed returns the module speed in MT/s. The configured (running)
// speed is preferred when available; otherwise the maximum rated speed is used.
// 0 means unknown.
func memoryDeviceSpeed(s smbiosStructure) uint64 {
	if v, ok := s.u16(off17ConfiguredSpeed); ok && v != 0 && v != 0xFFFF && v != 0x7FFF {
		return uint64(v)
	}
	if v, ok := s.u16(off17Speed); ok && v != 0 && v != 0xFFFF && v != 0x7FFF {
		return uint64(v)
	}
	return 0
}

// memoryType decodes the Memory Type enum (offset 0x12), SMBIOS spec 7.18.2.
func memoryType(v uint8) string {
	switch v {
	case 0x01:
		return "Other"
	case 0x02:
		return "Unknown"
	case 0x03:
		return "DRAM"
	case 0x04:
		return "EDRAM"
	case 0x05:
		return "VRAM"
	case 0x06:
		return "SRAM"
	case 0x07:
		return "RAM"
	case 0x08:
		return "ROM"
	case 0x09:
		return "FLASH"
	case 0x0A:
		return "EEPROM"
	case 0x0B:
		return "FEPROM"
	case 0x0C:
		return "EPROM"
	case 0x0D:
		return "CDRAM"
	case 0x0E:
		return "3DRAM"
	case 0x0F:
		return "SDRAM"
	case 0x10:
		return "SGRAM"
	case 0x11:
		return "RDRAM"
	case 0x12:
		return "DDR"
	case 0x13:
		return "DDR2"
	case 0x14:
		return "DDR2 FB-DIMM"
	case 0x18:
		return "DDR3"
	case 0x19:
		return "FBD2"
	case 0x1A:
		return "DDR4"
	case 0x1B:
		return "LPDDR"
	case 0x1C:
		return "LPDDR2"
	case 0x1D:
		return "LPDDR3"
	case 0x1E:
		return "LPDDR4"
	case 0x1F:
		return "Logical non-volatile device"
	case 0x20:
		return "HBM"
	case 0x21:
		return "HBM2"
	case 0x22:
		return "DDR5"
	case 0x23:
		return "LPDDR5"
	case 0x24:
		return "HBM3"
	default:
		return "Unknown"
	}
}

// memoryFormFactor decodes the Form Factor enum (offset 0x0E), SMBIOS spec 7.18.1.
func memoryFormFactor(v uint8) string {
	switch v {
	case 0x01:
		return "Other"
	case 0x02:
		return "Unknown"
	case 0x03:
		return "SIMM"
	case 0x04:
		return "SIP"
	case 0x05:
		return "Chip"
	case 0x06:
		return "DIP"
	case 0x07:
		return "ZIP"
	case 0x08:
		return "Proprietary Card"
	case 0x09:
		return "DIMM"
	case 0x0A:
		return "TSOP"
	case 0x0B:
		return "Row of chips"
	case 0x0C:
		return "RIMM"
	case 0x0D:
		return "SODIMM"
	case 0x0E:
		return "SRIMM"
	case 0x0F:
		return "FB-DIMM"
	case 0x10:
		return "Die"
	default:
		return "Unknown"
	}
}
