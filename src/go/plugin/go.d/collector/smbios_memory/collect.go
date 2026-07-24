// SPDX-License-Identifier: GPL-3.0-or-later

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

	for _, d := range devices {
		if !d.present {
			// Empty slot: leave a gap rather than emitting zeroed inventory.
			continue
		}

		labels := []string{
			c.dimmLocation(d),
			d.bankLocator,
			d.memoryType,
			d.formFactor,
			d.manufacturer,
			d.partNumber,
		}

		c.mx.dimmSize.WithLabelValues(labels...).Observe(float64(d.sizeBytes))
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

// dimmLocation returns a stable identity for a DIMM, falling back to the SMBIOS
// handle when the firmware does not provide a device locator.
func (c *Collector) dimmLocation(d memoryDevice) string {
	if d.locator != "" {
		return d.locator
	}
	return fmt.Sprintf("handle_%#04x", d.handle)
}
