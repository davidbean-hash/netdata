// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package smbios_memory

import (
	"context"
	_ "embed"
	"errors"

	"github.com/netdata/netdata/go/plugins/pkg/metrix"
	"github.com/netdata/netdata/go/plugins/plugin/framework/collectorapi"
)

//go:embed "config_schema.json"
var configSchema string

//go:embed "charts.yaml"
var chartTemplateYAML string

func init() {
	collectorapi.Register("smbios_memory", collectorapi.Creator{
		JobConfigSchema: configSchema,
		Defaults: collectorapi.Defaults{
			UpdateEvery: 60,
			// Opt-in: reading the raw DMI table usually requires elevated
			// permissions, so the module does not run unless explicitly enabled.
			Disabled: true,
		},
		CreateV2: func() collectorapi.CollectorV2 { return New() },
		Config:   func() any { return &Config{} },
	})
}

func New() *Collector {
	store := metrix.NewCollectorStore()
	mx := newCollectorMetrics(store)

	return &Collector{
		Config: Config{
			Path: defaultDMIPath,
		},
		store: store,
		mx:    mx,
	}
}

type Collector struct {
	collectorapi.Base
	Config `yaml:",inline" json:""`

	store metrix.CollectorStore
	mx    *collectorMetrics
}

func (c *Collector) Configuration() any {
	return c.Config
}

func (c *Collector) Init(context.Context) error {
	if c.Path == "" {
		return errors.New("config: 'path' to the SMBIOS/DMI table is not set")
	}
	c.Debugf("using DMI table path: %s", c.Path)
	return nil
}

func (c *Collector) Check(context.Context) error {
	devices, err := c.readMemoryDevices()
	if err != nil {
		return err
	}
	if len(devices) == 0 {
		return errors.New("no SMBIOS memory devices (DMI type 17) found")
	}
	return nil
}

func (c *Collector) Collect(context.Context) error {
	return c.collect()
}

func (c *Collector) MetricStore() metrix.CollectorStore { return c.store }

func (c *Collector) ChartTemplateYAML() string { return chartTemplateYAML }

func (c *Collector) Cleanup(context.Context) {}
