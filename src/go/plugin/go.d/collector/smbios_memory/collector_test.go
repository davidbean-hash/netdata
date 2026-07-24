// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package smbios_memory

import (
	"context"
	"os"
	"testing"

	"github.com/netdata/netdata/go/plugins/pkg/metrix"
	"github.com/netdata/netdata/go/plugins/plugin/framework/chartengine"
	"github.com/netdata/netdata/go/plugins/plugin/framework/charttpl"
	"github.com/netdata/netdata/go/plugins/plugin/go.d/pkg/collecttest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	dataConfigJSON, _ = os.ReadFile("testdata/config.json")
	dataConfigYAML, _ = os.ReadFile("testdata/config.yaml")
)

func TestConfigurationSerialize(t *testing.T) {
	require.NotEmpty(t, dataConfigJSON)
	require.NotEmpty(t, dataConfigYAML)
	collecttest.TestConfigurationSerialize(t, &Collector{}, dataConfigJSON, dataConfigYAML)
}

func TestCollector_Init(t *testing.T) {
	tests := map[string]struct {
		config   Config
		wantFail bool
	}{
		"success with default config": {config: Config{Path: defaultDMIPath}},
		"fail with empty path":        {wantFail: true, config: Config{Path: ""}},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			collr := New()
			collr.Config = test.config
			if test.wantFail {
				assert.Error(t, collr.Init(context.Background()))
			} else {
				assert.NoError(t, collr.Init(context.Background()))
			}
		})
	}
}

func TestCollector_Check(t *testing.T) {
	tests := map[string]struct {
		path     string
		wantFail bool
	}{
		"valid table with memory devices": {path: "testdata/type17.bin"},
		"missing file":                    {path: "testdata/does-not-exist.bin", wantFail: true},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			collr := New()
			collr.Config = Config{Path: test.path}
			require.NoError(t, collr.Init(context.Background()))
			if test.wantFail {
				assert.Error(t, collr.Check(context.Background()))
			} else {
				assert.NoError(t, collr.Check(context.Background()))
			}
		})
	}
}

func TestCollector_Collect(t *testing.T) {
	collr := New()
	collr.Config = Config{Path: "testdata/type17.bin"}

	require.NoError(t, collr.Init(context.Background()))
	require.NoError(t, collr.Check(context.Background()))

	cc := mustCycleController(t, collr.MetricStore())
	cc.BeginCycle()
	require.NoError(t, collr.Collect(context.Background()))
	cc.CommitCycleSuccess()

	r := collr.MetricStore().Read(metrix.ReadRaw())

	a1 := metrix.Labels{
		"location":     "DIMM_A1",
		"bank_locator": "BANK 0",
		"memory_type":  "DDR4",
		"form_factor":  "SODIMM",
		"manufacturer": "Samsung",
		"part_number":  "M471A2K43DB1-CWE",
	}
	assertValue(t, r, "dimm_size", a1, float64(16*gib))
	assertValue(t, r, "dimm_speed", a1, 2933)
	assertValue(t, r, "dimm_ranks", a1, 2)

	b1 := metrix.Labels{
		"location":     "DIMM_B1",
		"bank_locator": "BANK 2",
		"memory_type":  "DDR5",
		"form_factor":  "DIMM",
		"manufacturer": "Micron",
		"part_number":  "MTC20C2085S1EC48BA1",
	}
	assertValue(t, r, "dimm_size", b1, float64(32*gib))
	assertValue(t, r, "dimm_speed", b1, 4800)
	assertValue(t, r, "dimm_ranks", b1, 1)

	// DIMM_C1: short structure -> no ranks metric emitted.
	c1 := metrix.Labels{
		"location":     "DIMM_C1",
		"bank_locator": "BANK 3",
		"memory_type":  "DDR3",
		"form_factor":  "DIMM",
		"manufacturer": "Hynix",
		"part_number":  "HMT41GS6BFR8C-PB",
	}
	assertValue(t, r, "dimm_size", c1, float64(8*gib))
	assertValue(t, r, "dimm_speed", c1, 1600)
	_, hasRanks := r.Value("dimm_ranks", c1)
	assert.False(t, hasRanks, "short structure should not emit ranks")

	// DIMM_A2 is an empty slot: no series of any kind should exist for it.
	a2 := metrix.Labels{"location": "DIMM_A2", "bank_locator": "BANK 1"}
	_, hasEmpty := r.Value("dimm_size", a2)
	assert.False(t, hasEmpty, "empty slot must not emit metrics")

	collecttest.AssertChartCoverage(t, collr, collecttest.ChartCoverageExpectation{})
}

func TestCollector_Cleanup(t *testing.T) {
	collr := New()
	assert.NotPanics(t, func() { collr.Cleanup(context.Background()) })
}

func TestCollector_ChartTemplateYAML(t *testing.T) {
	templateYAML := New().ChartTemplateYAML()
	collecttest.AssertChartTemplateSchema(t, templateYAML)

	spec, err := charttpl.DecodeYAML([]byte(templateYAML))
	require.NoError(t, err)
	require.NoError(t, spec.Validate())

	_, err = chartengine.Compile(spec, 1)
	require.NoError(t, err)
}

func mustCycleController(t *testing.T, store metrix.CollectorStore) metrix.CycleController {
	t.Helper()
	managed, ok := metrix.AsCycleManagedStore(store)
	require.True(t, ok, "store does not expose cycle control")
	return managed.CycleController()
}

func assertValue(t *testing.T, r metrix.Reader, name string, labels metrix.Labels, want float64) {
	t.Helper()
	got, ok := r.Value(name, labels)
	require.Truef(t, ok, "expected metric %s labels=%v", name, labels)
	assert.InDeltaf(t, want, got, 1e-9, "unexpected value for %s labels=%v", name, labels)
}
