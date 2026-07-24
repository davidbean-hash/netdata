use super::*;

/// Configuration for the rule-based rollup-to-metrics layer.
///
/// User-defined rollup rules aggregate decoded flows into standard Netdata
/// charts every collection cycle. Once a rollup is a standard metric, Netdata
/// alerting and per-dimension ML anomaly detection apply to it automatically.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct RollupsConfig {
    /// Master switch for the rollup layer. When false, no rollup charts are
    /// emitted regardless of the configured rules.
    #[serde(default = "default_true")]
    pub(crate) enabled: bool,

    /// User-defined rollup rules. Each rule becomes one chart.
    #[serde(default)]
    pub(crate) rules: Vec<RollupRuleConfig>,
}

impl Default for RollupsConfig {
    fn default() -> Self {
        Self {
            enabled: true,
            rules: Vec::new(),
        }
    }
}

/// A single rollup rule.
///
/// A rule selects the flows it cares about (`filters`), optionally splits them
/// into one dimension per distinct value of a flow field (`group_by`), and sums
/// a counter (`metric`) over each cycle. Counters are emitted cumulatively and
/// declared with the `incremental` dimension algorithm, so Netdata derives the
/// per-second rate (bytes/s, packets/s, flows/s).
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct RollupRuleConfig {
    /// Rule name. Used to build the chart id (`netflow.rollup_<name>`), so it
    /// must be unique across rules.
    pub(crate) name: String,

    /// Optional canonical flow field to split the metric by (for example
    /// `SRC_COUNTRY`). When omitted, the rule emits a single `all` dimension
    /// summing every matching flow.
    #[serde(default)]
    pub(crate) group_by: Option<String>,

    /// Optional allow-list filters keyed by canonical flow field name. A flow
    /// matches when, for every listed field, its value is one of the listed
    /// values (AND across fields, OR within a field). An empty map matches all
    /// flows.
    #[serde(default)]
    pub(crate) filters: BTreeMap<String, Vec<String>>,

    /// The counter aggregated by this rule.
    #[serde(default)]
    pub(crate) metric: RollupMetric,

    /// Upper bound on the number of distinct `group_by` values that get their
    /// own dimension. Additional values are folded into a single `__overflow__`
    /// dimension, mirroring the interactive query group-by overflow behavior.
    #[serde(default = "default_rollup_max_cardinality")]
    pub(crate) max_cardinality: usize,
}

/// The flow counter a rollup rule aggregates.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, Default)]
#[serde(rename_all = "snake_case")]
pub(crate) enum RollupMetric {
    /// Sum of `BYTES`, emitted as bytes/s.
    #[default]
    Bytes,
    /// Sum of `PACKETS`, emitted as packets/s.
    Packets,
    /// Sum of `FLOWS`, emitted as flows/s.
    Flows,
}

impl RollupMetric {
    /// Chart units for this metric.
    pub(crate) fn units(self) -> &'static str {
        match self {
            RollupMetric::Bytes => "bytes/s",
            RollupMetric::Packets => "packets/s",
            RollupMetric::Flows => "flows/s",
        }
    }

    /// Extract this metric's contribution from a decoded flow record.
    pub(crate) fn value(self, record: &crate::flow::FlowRecord) -> u64 {
        match self {
            RollupMetric::Bytes => record.bytes,
            RollupMetric::Packets => record.packets,
            RollupMetric::Flows => record.flows,
        }
    }
}
