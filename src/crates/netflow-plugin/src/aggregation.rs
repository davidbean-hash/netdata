//! Config-driven rollup-to-metrics layer.
//!
//! The interactive `flows:netflow` function can group and filter flows on
//! demand, but those aggregations are never pinned as standard metrics. This
//! module lets users declare rollup rules in configuration; every collection
//! cycle the configured aggregations are computed over the decoded flows and
//! emitted as ordinary Netdata charts. Because the results are standard
//! metrics, Netdata alerting and per-dimension ML anomaly detection apply to
//! them automatically.
//!
//! Two pieces cooperate:
//! - [`RollupEngine`] observes every decoded flow record on the ingest path and
//!   maintains cumulative counters per rule and per group value.
//! - [`RollupEmitter`] runs on its own cadence, snapshots the engine, and
//!   renders the Netdata chart protocol (`CHART`/`DIMENSION`/`BEGIN`/`SET`/
//!   `END`). Counters are emitted cumulatively with the `incremental`
//!   dimension algorithm, so Netdata derives per-second rates.

use crate::facet_runtime::{FacetValueSink, append_record_facet_values};
use crate::flow::{FlowRecord, intern_field_name};
use crate::plugin_config::{RollupMetric, RollupsConfig};
use std::collections::{BTreeMap, BTreeSet, HashSet};
use std::net::IpAddr;
use std::sync::Mutex;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

/// Dimension used when a rule has no `group_by` field: the whole matching set.
const TOTAL_DIMENSION: &str = "all";
/// Dimension label for an empty `group_by` value (for example an unresolved
/// GeoIP country).
const UNKNOWN_GROUP_LABEL: &str = "unknown";
/// Synthetic dimension collecting groups beyond `max_cardinality`. Matches the
/// interactive query group-by overflow bucket name.
const OVERFLOW_GROUP_LABEL: &str = "__overflow__";
/// Chart priority placing rollup charts after the built-in diagnostic charts.
const ROLLUP_CHART_PRIORITY: i64 = 90_000;

/// A rollup rule compiled from configuration into a form efficient to evaluate
/// on the ingest hot path.
#[derive(Debug, Clone)]
struct CompiledRule {
    name: String,
    group_by: Option<&'static str>,
    filters: Vec<(&'static str, Vec<String>)>,
    metric: RollupMetric,
    max_cardinality: usize,
}

impl CompiledRule {
    fn from_config(rule: &crate::plugin_config::RollupRuleConfig) -> Self {
        let group_by = rule.group_by.as_deref().and_then(intern_field_name);
        let filters = rule
            .filters
            .iter()
            .filter_map(|(field, values)| {
                intern_field_name(field).map(|field| (field, values.clone()))
            })
            .collect();
        Self {
            name: rule.name.clone(),
            group_by,
            filters,
            metric: rule.metric,
            max_cardinality: rule.max_cardinality.max(1),
        }
    }

    /// Fields this rule reads from each record (filters + group_by).
    fn referenced_fields(&self) -> impl Iterator<Item = &'static str> + '_ {
        self.filters
            .iter()
            .map(|(field, _)| *field)
            .chain(self.group_by)
    }

    /// Whether a flow (already reduced to the wanted field values) matches every
    /// filter of this rule.
    fn matches(&self, fields: &BTreeMap<&'static str, String>) -> bool {
        self.filters.iter().all(|(field, allowed)| {
            let value = fields.get(field).map(String::as_str).unwrap_or_default();
            allowed.iter().any(|candidate| candidate == value)
        })
    }

    /// The group label for a matching flow: the (empty-normalized) value of the
    /// `group_by` field, or the single total dimension when there is no
    /// `group_by`.
    fn group_label(&self, fields: &BTreeMap<&'static str, String>) -> String {
        match self.group_by {
            None => TOTAL_DIMENSION.to_string(),
            Some(field) => {
                let value = fields.get(field).map(String::as_str).unwrap_or_default();
                if value.is_empty() {
                    UNKNOWN_GROUP_LABEL.to_string()
                } else {
                    value.to_string()
                }
            }
        }
    }
}

/// Per-rule cumulative counters keyed by group label.
#[derive(Debug, Default, Clone)]
struct RuleState {
    groups: BTreeMap<String, u64>,
    overflow: u64,
    overflowed: bool,
}

impl RuleState {
    /// Add `amount` to `label`, folding into the overflow bucket once the
    /// distinct-label budget is exhausted.
    fn add(&mut self, label: String, amount: u64, max_cardinality: usize) {
        if let Some(counter) = self.groups.get_mut(&label) {
            *counter = counter.saturating_add(amount);
            return;
        }
        if self.groups.len() < max_cardinality {
            self.groups.insert(label, amount);
        } else {
            self.overflowed = true;
            self.overflow = self.overflow.saturating_add(amount);
        }
    }

    /// Snapshot of the current cumulative counters, including the overflow
    /// dimension when it is in use, ordered deterministically.
    fn dimensions(&self) -> Vec<(String, u64)> {
        let mut dims: Vec<(String, u64)> = self
            .groups
            .iter()
            .map(|(label, value)| (label.clone(), *value))
            .collect();
        if self.overflowed {
            dims.push((OVERFLOW_GROUP_LABEL.to_string(), self.overflow));
        }
        dims
    }
}

/// A point-in-time view of one rule's chart.
#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) struct RuleSnapshot {
    pub(crate) name: String,
    pub(crate) metric: RollupMetric,
    pub(crate) grouped: bool,
    pub(crate) dimensions: Vec<(String, u64)>,
}

/// Sink that captures the string form of a fixed set of flow fields, reusing
/// the canonical record→field mapping so no field logic is duplicated here.
struct FieldCaptureSink<'a> {
    wanted: &'a HashSet<&'static str>,
    out: &'a mut BTreeMap<&'static str, String>,
}

impl<'a> FieldCaptureSink<'a> {
    fn wants(&self, field: &'static str) -> bool {
        self.wanted.contains(field)
    }
}

impl FacetValueSink for FieldCaptureSink<'_> {
    fn insert_text_static(&mut self, field: &'static str, value: &str) {
        if self.wants(field) {
            self.out.insert(field, value.to_string());
        }
    }
    fn insert_u8_static(&mut self, field: &'static str, value: u8) {
        if self.wants(field) {
            self.out.insert(field, value.to_string());
        }
    }
    fn insert_u8_present_static(&mut self, field: &'static str, value: u8) {
        if self.wants(field) {
            self.out.insert(field, value.to_string());
        }
    }
    fn insert_u16_static(&mut self, field: &'static str, value: u16) {
        if self.wants(field) {
            self.out.insert(field, value.to_string());
        }
    }
    fn insert_u32_static(&mut self, field: &'static str, value: u32) {
        if self.wants(field) {
            self.out.insert(field, value.to_string());
        }
    }
    fn insert_u64_static(&mut self, field: &'static str, value: u64) {
        if self.wants(field) {
            self.out.insert(field, value.to_string());
        }
    }
    fn insert_ip_static(&mut self, field: &'static str, value: Option<IpAddr>) {
        if let Some(addr) = value
            && self.wants(field)
        {
            self.out.insert(field, addr.to_string());
        }
    }
}

/// Aggregates decoded flows into per-rule cumulative counters.
pub(crate) struct RollupEngine {
    rules: Vec<CompiledRule>,
    wanted_fields: HashSet<&'static str>,
    states: Mutex<Vec<RuleState>>,
}

impl RollupEngine {
    /// Build an engine from configuration, or `None` when the layer is disabled
    /// or no rules are configured.
    pub(crate) fn from_config(config: &RollupsConfig) -> Option<Self> {
        if !config.enabled || config.rules.is_empty() {
            return None;
        }
        let rules: Vec<CompiledRule> = config.rules.iter().map(CompiledRule::from_config).collect();
        let wanted_fields: HashSet<&'static str> = rules
            .iter()
            .flat_map(CompiledRule::referenced_fields)
            .collect();
        let states = Mutex::new(vec![RuleState::default(); rules.len()]);
        Some(Self {
            rules,
            wanted_fields,
            states,
        })
    }

    /// Observe a single decoded flow, updating every matching rule's counters.
    pub(crate) fn observe(&self, record: &FlowRecord) {
        let mut fields: BTreeMap<&'static str, String> = BTreeMap::new();
        if !self.wanted_fields.is_empty() {
            let mut sink = FieldCaptureSink {
                wanted: &self.wanted_fields,
                out: &mut fields,
            };
            append_record_facet_values(&mut sink, record);
        }

        let mut states = match self.states.lock() {
            Ok(states) => states,
            Err(poisoned) => poisoned.into_inner(),
        };
        for (rule, state) in self.rules.iter().zip(states.iter_mut()) {
            if !rule.matches(&fields) {
                continue;
            }
            let amount = rule.metric.value(record);
            if amount == 0 {
                // Still register the group so its dimension exists once it has
                // seen a matching flow; a zero delta keeps the rate at zero.
                let label = rule.group_label(&fields);
                state.add(label, 0, rule.max_cardinality);
                continue;
            }
            let label = rule.group_label(&fields);
            state.add(label, amount, rule.max_cardinality);
        }
    }

    /// Snapshot every rule's current counters for emission.
    pub(crate) fn snapshot(&self) -> Vec<RuleSnapshot> {
        let states = match self.states.lock() {
            Ok(states) => states,
            Err(poisoned) => poisoned.into_inner(),
        };
        self.rules
            .iter()
            .zip(states.iter())
            .map(|(rule, state)| RuleSnapshot {
                name: rule.name.clone(),
                metric: rule.metric,
                grouped: rule.group_by.is_some(),
                dimensions: state.dimensions(),
            })
            .collect()
    }
}

/// Renders rollup snapshots into the Netdata chart protocol, tracking which
/// charts and dimensions have already been defined so definitions are (re)sent
/// only when a new dimension appears.
pub(crate) struct RollupEmitter {
    interval: Duration,
    last_emit: Option<SystemTime>,
    charts: Vec<ChartEmitState>,
}

struct ChartEmitState {
    chart_id: String,
    context: String,
    title: String,
    units: &'static str,
    chart_type: &'static str,
    defined_dims: BTreeSet<String>,
}

impl RollupEmitter {
    pub(crate) fn new(snapshots: &[RuleSnapshot], interval: Duration) -> Self {
        let charts = snapshots
            .iter()
            .map(|snapshot| ChartEmitState {
                chart_id: format!("netflow.rollup_{}", sanitize_id(&snapshot.name)),
                context: format!("netdata.netflow.rollup_{}", sanitize_id(&snapshot.name)),
                title: format!("Netflow Rollup {}", escape_protocol_text(&snapshot.name)),
                units: snapshot.metric.units(),
                chart_type: if snapshot.grouped { "stacked" } else { "line" },
                defined_dims: BTreeSet::new(),
            })
            .collect();
        Self {
            interval,
            last_emit: None,
            charts,
        }
    }

    /// Render one collection cycle for all rules to Netdata protocol bytes.
    pub(crate) fn render(&mut self, snapshots: &[RuleSnapshot], now: SystemTime) -> String {
        // Report the actual time elapsed since the previous emission so Netdata
        // computes rates over the real interval even when a tick is skipped.
        let dt = match self.last_emit {
            Some(prev) => now.duration_since(prev).unwrap_or(self.interval),
            None => self.interval,
        };
        self.last_emit = Some(now);

        let mut out = String::new();
        for (chart, snapshot) in self.charts.iter_mut().zip(snapshots.iter()) {
            chart.render_into(&mut out, snapshot, self.interval, dt, now);
        }
        out
    }
}

impl ChartEmitState {
    fn render_into(
        &mut self,
        out: &mut String,
        snapshot: &RuleSnapshot,
        update_every: Duration,
        dt: Duration,
        now: SystemTime,
    ) {
        // Distinct raw group labels can sanitize to the same Netdata dimension
        // id (e.g. "a/b" and "a b" both become "a_b"). Emitting two `SET`s with
        // the same id in one `BEGIN`/`END` would let Netdata keep only the last,
        // silently dropping a group's counter. Merge collisions by summing their
        // cumulative values into a single dimension, keeping the first-seen label
        // as the display name. Ordered by id for deterministic output.
        let mut merged: BTreeMap<String, (String, u64)> = BTreeMap::new();
        for (label, value) in &snapshot.dimensions {
            let id = sanitize_id(label);
            let entry = merged.entry(id).or_insert_with(|| (label.clone(), 0));
            entry.1 = entry.1.saturating_add(*value);
        }

        let has_new_dim = merged.keys().any(|id| !self.defined_dims.contains(id));

        // (Re)send the chart definition whenever a new dimension appears. This
        // is how Netdata learns about dimensions added after the first cycle.
        if has_new_dim {
            out.push_str(&format!(
                "CHART {} '' '{}' '{}' 'rollups' '{}' {} {} {}\n",
                self.chart_id,
                self.title,
                self.units,
                self.context,
                self.chart_type,
                ROLLUP_CHART_PRIORITY,
                update_every.as_secs().max(1),
            ));
            for (id, (label, _)) in &merged {
                out.push_str(&format!(
                    "DIMENSION {} '{}' incremental 1 1\n",
                    id,
                    escape_protocol_text(label)
                ));
                self.defined_dims.insert(id.clone());
            }
        }

        if merged.is_empty() {
            return;
        }

        out.push_str(&format!(
            "BEGIN {} {}\n",
            self.chart_id,
            dt.as_micros().max(1),
        ));
        for (id, (_, value)) in &merged {
            out.push_str(&format!("SET {} = {}\n", id, clamp_to_i64(*value)));
        }
        let secs = now
            .duration_since(UNIX_EPOCH)
            .unwrap_or(Duration::ZERO)
            .as_secs();
        out.push_str(&format!("END {}\n", secs));
    }
}

/// Restrict a value to the `i64` range Netdata `SET` accepts.
fn clamp_to_i64(value: u64) -> i64 {
    value.min(i64::MAX as u64) as i64
}

/// Neutralize a human-readable string for embedding inside a single-quoted
/// Netdata protocol argument (chart title, dimension display name). Group
/// labels and titles derive from untrusted flow enrichment (exporter/BGP/GeoIP
/// strings) which may contain the `'` delimiter or newlines that would
/// otherwise terminate or inject into the plugin protocol stream.
fn escape_protocol_text(raw: &str) -> String {
    let cleaned: String = raw
        .chars()
        .map(|c| if c.is_control() || c == '\'' { ' ' } else { c })
        .collect();
    let trimmed = cleaned.trim();
    if trimmed.is_empty() {
        UNKNOWN_GROUP_LABEL.to_string()
    } else {
        trimmed.to_string()
    }
}

/// Reduce an arbitrary label to a Netdata-safe chart/dimension identifier.
pub(crate) fn sanitize_id(raw: &str) -> String {
    let mut out: String = raw
        .chars()
        .map(|c| {
            if c.is_ascii_alphanumeric() || matches!(c, '_' | '-' | '.') {
                c
            } else {
                '_'
            }
        })
        .collect();
    if out.is_empty() {
        out.push_str(UNKNOWN_GROUP_LABEL);
    }
    out
}

#[cfg(test)]
#[path = "aggregation_tests.rs"]
mod tests;
