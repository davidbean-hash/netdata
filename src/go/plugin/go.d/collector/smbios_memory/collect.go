// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package smbios_memory

import (
	"fmt"
	"os"
)

func (c *Collector) collect() error {
	devices, err := c.readMemoryDevices()
	if err != nil {
		return err
	}

	// Device locators are normally unique per slot; disambiguate any duplicates
	// from a non-conforming BIOS with the SMBIOS handle so each populated DIMM
	// maps to its own chart instance instead of colliding on one.
	locatorCount := map[string]int{}
	for _, d := range devices {
		if d.present {
			locatorCount[d.locator]++
		}
	}

	for _, d := range devices {
		if !d.present {
			// Empty slot: leave a gap rather than emitting zeroed inventory.
			continue
		}

		labels := []string{
			c.dimmLocation(d, locatorCount[d.locator] > 1),
			d.bankLocator,
			d.memoryType,
			d.formFactor,
			d.manufacturer,
			d.partNumber,
		}

		if d.sizeBytes > 0 {
			c.mx.dimmSize.WithLabelValues(labels...).Observe(float64(d.sizeBytes))
		}
		if d.speedMTs > 0 {
			c.mx.dimmSpeed.WithLabelValues(labels...).Observe(float64(d.speedMTs))
		}
		if d.ranks > 0 {
			c.mx.dimmRanks.WithLabelValues(labels...).Observe(float64(d.ranks))
		}
	}

	return nil
}

// readMemoryDevices reads and parses the raw SMBIOS table and returns all
// Memory Device (type 17) records, including empty slots.
func (c *Collector) readMemoryDevices() ([]memoryDevice, error) {
	b, err := os.ReadFile(c.Path)
	if err != nil {
		return nil, fmt.Errorf("reading DMI table %q: %v", c.Path, err)
	}

	structs, err := parseSMBIOSStructures(b)
	if err != nil {
		return nil, fmt.Errorf("parsing SMBIOS table %q: %v", c.Path, err)
	}

	var devices []memoryDevice
	for _, s := range structs {
		if s.stype == smbiosTypeMemoryDevice {
			devices = append(devices, parseMemoryDevice(s))
		}
	}

	return devices, nil
}

// dimmLocation returns a stable identity for a DIMM. It falls back to the SMBIOS
// handle when the firmware provides no device locator, and appends the handle
// when the locator is not unique among populated slots.
func (c *Collector) dimmLocation(d memoryDevice, ambiguous bool) string {
	if d.locator == "" {
		return fmt.Sprintf("handle_%#04x", d.handle)
	}
	if ambiguous {
		return fmt.Sprintf("%s_%#04x", d.locator, d.handle)
	}
	return d.locator
}
