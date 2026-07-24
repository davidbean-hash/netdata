#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Generates a synthetic SMBIOS structure table (as found in
# /sys/firmware/dmi/tables/DMI) containing several Memory Device (type 17)
# records for use as a parser test fixture. No real hardware is involved.
#
# Regenerate with:
#     python3 gen.py
#
import struct


def structure(stype, handle, formatted, strings):
    # header: type, length, handle(LE). length counts the formatted area incl. header.
    length = 4 + len(formatted)
    out = bytearray()
    out += struct.pack("<BBH", stype, length, handle)
    out += formatted
    if strings:
        for s in strings:
            out += s.encode("ascii") + b"\x00"
        out += b"\x00"  # set terminator
    else:
        out += b"\x00\x00"  # no strings -> double NUL
    return bytes(out)


def mem_device(handle, *, size_word, ext_size=0, form_factor, dev_locator,
               bank_locator, mem_type, speed, manufacturer, part_number,
               attributes, configured_speed, strings, length=0x28):
    # Build a full-length (SMBIOS 2.8) formatted area then trim to `length`.
    f = bytearray(length - 4)  # formatted area excludes the 4-byte header

    def setb(off, val):
        f[off - 4] = val & 0xFF

    def setw(off, val):
        f[off - 4] = val & 0xFF
        f[off - 3] = (val >> 8) & 0xFF

    def setl(off, val):
        for i in range(4):
            f[off - 4 + i] = (val >> (8 * i)) & 0xFF

    # string indexes are 1-based; 0 = not specified
    def sidx(name):
        return 0 if name is None else strings.index(name) + 1

    setw(0x08, 64)                 # total width
    setw(0x0A, 64)                 # data width
    setw(0x0C, size_word)          # size
    setb(0x0E, form_factor)        # form factor
    setb(0x10, sidx(dev_locator))  # device locator
    setb(0x11, sidx(bank_locator)) # bank locator
    setb(0x12, mem_type)           # memory type
    setw(0x15, speed)              # max speed
    setb(0x17, sidx(manufacturer)) # manufacturer
    setb(0x1A, sidx(part_number))  # part number
    if length > 0x1B:
        setb(0x1B, attributes)     # attributes (rank)
    if length >= 0x20:
        setl(0x1C, ext_size)       # extended size
    if length >= 0x22:
        setw(0x20, configured_speed)  # configured speed
    return structure(17, handle, bytes(f), strings)


blob = bytearray()

# DIMM 0: populated 16 GB DDR4 SODIMM, dual rank, 3200 MT/s configured 2933.
blob += mem_device(
    0x0010, size_word=16384, form_factor=0x0D, dev_locator="DIMM_A1",
    bank_locator="BANK 0", mem_type=0x1A, speed=3200, manufacturer="Samsung",
    part_number="M471A2K43DB1-CWE", attributes=0x02, configured_speed=2933,
    strings=["DIMM_A1", "BANK 0", "Samsung", "M471A2K43DB1-CWE"])

# DIMM 1: empty slot (size 0), no strings for the module fields.
blob += mem_device(
    0x0011, size_word=0, form_factor=0x0D, dev_locator="DIMM_A2",
    bank_locator="BANK 1", mem_type=0x02, speed=0, manufacturer=None,
    part_number=None, attributes=0x00, configured_speed=0,
    strings=["DIMM_A2", "BANK 1"])

# DIMM 2: populated 32 GB DDR5 DIMM using the Extended Size field (size word 0x7FFF).
blob += mem_device(
    0x0012, size_word=0x7FFF, ext_size=32768, form_factor=0x09,
    dev_locator="DIMM_B1", bank_locator="BANK 2", mem_type=0x22, speed=5600,
    manufacturer="Micron", part_number="MTC20C2085S1EC48BA1",
    attributes=0x01, configured_speed=4800,
    strings=["DIMM_B1", "BANK 2", "Micron", "MTC20C2085S1EC48BA1"])

# DIMM 3: populated 8 GB DDR3, older/shorter structure (SMBIOS 2.3, length 0x1B,
# no attributes/extended-size/configured-speed fields).
blob += mem_device(
    0x0013, size_word=8192, form_factor=0x09, dev_locator="DIMM_C1",
    bank_locator="BANK 3", mem_type=0x18, speed=1600, manufacturer="Hynix",
    part_number="HMT41GS6BFR8C-PB", attributes=0x00, configured_speed=0,
    strings=["DIMM_C1", "BANK 3", "Hynix", "HMT41GS6BFR8C-PB"], length=0x1B)

# End-of-table (type 127).
blob += structure(127, 0x0014, b"", [])

with open("type17.bin", "wb") as fh:
    fh.write(blob)

print("wrote type17.bin, %d bytes" % len(blob))
