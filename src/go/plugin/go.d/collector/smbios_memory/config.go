// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package smbios_memory

// defaultDMIPath is the kernel-exported raw SMBIOS structure table. It holds
// the same bytes that `dmidecode` reads and is typically root-readable only.
const defaultDMIPath = "/sys/firmware/dmi/tables/DMI"

type Config struct {
	Vnode              string `yaml:"vnode,omitempty" json:"vnode"`
	UpdateEvery        int    `yaml:"update_every,omitempty" json:"update_every"`
	AutoDetectionRetry int    `yaml:"autodetection_retry,omitempty" json:"autodetection_retry"`
	Path               string `yaml:"path,omitempty" json:"path"`
}
