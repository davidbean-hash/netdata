// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package smbios_memory

import "github.com/netdata/netdata/go/plugins/pkg/metrix"

// Label keys attached to every per-DIMM series. "location" is the stable
// instance identity; the rest are descriptive labels promoted onto the series.
var dimmLabelKeys = []string{
	"location",
	"bank_locator",
	"memory_type",
	"form_factor",
	"manufacturer",
	"part_number",
}

type collectorMetrics struct {
	dimmSize  metrix.SnapshotGaugeVec
	dimmSpeed metrix.SnapshotGaugeVec
	dimmRanks metrix.SnapshotGaugeVec
}

func newCollectorMetrics(store metrix.CollectorStore) *collectorMetrics {
	meter := store.Write().SnapshotMeter("")
	vec := meter.Vec(dimmLabelKeys...)

	return &collectorMetrics{
		dimmSize:  vec.Gauge("dimm_size"),
		dimmSpeed: vec.Gauge("dimm_speed"),
		dimmRanks: vec.Gauge("dimm_ranks"),
	}
}
