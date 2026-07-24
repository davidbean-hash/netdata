use super::*;
use crate::plugin_config::RollupRuleConfig;

fn record(country: &str, bytes: u64, packets: u64) -> FlowRecord {
    FlowRecord {
        src_country: country.to_string(),
        bytes,
        packets,
        flows: 1,
        ..Default::default()
    }
}

fn rule(
    name: &str,
    group_by: Option<&str>,
    filters: &[(&str, &[&str])],
    metric: RollupMetric,
    max_cardinality: usize,
) -> RollupRuleConfig {
    RollupRuleConfig {
        name: name.to_string(),
        group_by: group_by.map(str::to_string),
        filters: filters
            .iter()
            .map(|(field, values)| {
                (
                    field.to_string(),
                    values.iter().map(|v| v.to_string()).collect(),
                )
            })
            .collect(),
        metric,
        max_cardinality,
    }
}

fn config(rules: Vec<RollupRuleConfig>) -> RollupsConfig {
    RollupsConfig {
        enabled: true,
        rules,
    }
}

#[test]
fn engine_disabled_or_empty_returns_none() {
    let mut cfg = config(vec![rule(
        "r",
        Some("SRC_COUNTRY"),
        &[],
        RollupMetric::Bytes,
        10,
    )]);
    cfg.enabled = false;
    assert!(RollupEngine::from_config(&cfg).is_none());

    let empty = config(vec![]);
    assert!(RollupEngine::from_config(&empty).is_none());
}

#[test]
fn group_by_sums_metric_per_value() {
    let cfg = config(vec![rule(
        "bytes_by_country",
        Some("SRC_COUNTRY"),
        &[],
        RollupMetric::Bytes,
        100,
    )]);
    let engine = RollupEngine::from_config(&cfg).unwrap();

    engine.observe(&record("IR", 100, 1));
    engine.observe(&record("US", 200, 1));
    engine.observe(&record("IR", 50, 1));

    let snapshot = engine.snapshot();
    assert_eq!(snapshot.len(), 1);
    assert!(snapshot[0].grouped);
    assert_eq!(
        snapshot[0].dimensions,
        vec![("IR".to_string(), 150), ("US".to_string(), 200)]
    );
}

#[test]
fn filter_restricts_matching_flows_and_totals_without_group_by() {
    let cfg = config(vec![rule(
        "bytes_from_iran",
        None,
        &[("SRC_COUNTRY", &["IR"])],
        RollupMetric::Bytes,
        100,
    )]);
    let engine = RollupEngine::from_config(&cfg).unwrap();

    engine.observe(&record("IR", 100, 1));
    engine.observe(&record("US", 200, 1));
    engine.observe(&record("IR", 50, 1));

    let snapshot = engine.snapshot();
    assert!(!snapshot[0].grouped);
    assert_eq!(snapshot[0].dimensions, vec![("all".to_string(), 150)]);
}

#[test]
fn filter_supports_multiple_allowed_values() {
    let cfg = config(vec![rule(
        "eu",
        Some("SRC_COUNTRY"),
        &[("SRC_COUNTRY", &["DE", "FR"])],
        RollupMetric::Packets,
        100,
    )]);
    let engine = RollupEngine::from_config(&cfg).unwrap();

    engine.observe(&record("DE", 10, 5));
    engine.observe(&record("FR", 10, 7));
    engine.observe(&record("US", 10, 9));

    let snapshot = engine.snapshot();
    assert_eq!(
        snapshot[0].dimensions,
        vec![("DE".to_string(), 5), ("FR".to_string(), 7)]
    );
}

#[test]
fn counters_are_cumulative_across_cycles() {
    let cfg = config(vec![rule(
        "bytes",
        Some("SRC_COUNTRY"),
        &[],
        RollupMetric::Bytes,
        100,
    )]);
    let engine = RollupEngine::from_config(&cfg).unwrap();

    engine.observe(&record("IR", 100, 1));
    assert_eq!(
        engine.snapshot()[0].dimensions,
        vec![("IR".to_string(), 100)]
    );

    engine.observe(&record("IR", 25, 1));
    assert_eq!(
        engine.snapshot()[0].dimensions,
        vec![("IR".to_string(), 125)]
    );
}

#[test]
fn cardinality_cap_folds_into_overflow_bucket() {
    let cfg = config(vec![rule(
        "capped",
        Some("SRC_COUNTRY"),
        &[],
        RollupMetric::Bytes,
        1,
    )]);
    let engine = RollupEngine::from_config(&cfg).unwrap();

    engine.observe(&record("IR", 100, 1));
    engine.observe(&record("US", 200, 1));
    engine.observe(&record("DE", 300, 1));

    let snapshot = engine.snapshot();
    assert_eq!(
        snapshot[0].dimensions,
        vec![("IR".to_string(), 100), ("__overflow__".to_string(), 500)]
    );
}

#[test]
fn empty_group_value_is_labeled_unknown() {
    let cfg = config(vec![rule(
        "bytes",
        Some("SRC_COUNTRY"),
        &[],
        RollupMetric::Bytes,
        100,
    )]);
    let engine = RollupEngine::from_config(&cfg).unwrap();

    engine.observe(&record("", 100, 1));

    assert_eq!(
        engine.snapshot()[0].dimensions,
        vec![("unknown".to_string(), 100)]
    );
}

#[test]
fn flows_metric_counts_flow_records() {
    let cfg = config(vec![rule(
        "flows",
        Some("SRC_COUNTRY"),
        &[],
        RollupMetric::Flows,
        100,
    )]);
    let engine = RollupEngine::from_config(&cfg).unwrap();

    engine.observe(&record("IR", 100, 1));
    engine.observe(&record("IR", 100, 1));

    assert_eq!(engine.snapshot()[0].dimensions, vec![("IR".to_string(), 2)]);
}

#[test]
fn emitter_defines_chart_then_updates_incrementally() {
    let cfg = config(vec![rule(
        "bytes_by_country",
        Some("SRC_COUNTRY"),
        &[],
        RollupMetric::Bytes,
        100,
    )]);
    let engine = RollupEngine::from_config(&cfg).unwrap();
    let now = UNIX_EPOCH + Duration::from_secs(100);

    engine.observe(&record("IR", 100, 1));
    let first = engine.snapshot();
    let mut emitter = RollupEmitter::new(&first, Duration::from_secs(1));

    let cycle1 = emitter.render(&first, now);
    assert!(cycle1.contains(
        "CHART netflow.rollup_bytes_by_country '' 'Netflow Rollup bytes_by_country' 'bytes/s' 'rollups' 'netflow.rollup_bytes_by_country' stacked 90000 1\n"
    ));
    assert!(cycle1.contains("DIMENSION IR 'IR' incremental 1 1\n"));
    assert!(cycle1.contains("BEGIN netflow.rollup_bytes_by_country 1000000\n"));
    assert!(cycle1.contains("SET IR = 100\n"));
    assert!(cycle1.contains("END 100\n"));

    // Second cycle, same dimensions: no re-definition, just an update.
    engine.observe(&record("IR", 50, 1));
    let second = engine.snapshot();
    let cycle2 = emitter.render(&second, now);
    assert!(!cycle2.contains("CHART "));
    assert!(!cycle2.contains("DIMENSION "));
    assert!(cycle2.contains("SET IR = 150\n"));

    // A brand-new dimension re-sends the chart definition.
    engine.observe(&record("US", 200, 1));
    let third = engine.snapshot();
    let cycle3 = emitter.render(&third, now);
    assert!(cycle3.contains("CHART netflow.rollup_bytes_by_country"));
    assert!(cycle3.contains("DIMENSION US 'US' incremental 1 1\n"));
    assert!(cycle3.contains("SET US = 200\n"));
}

#[test]
fn emitter_uses_trimmed_rule_name_for_chart_id() {
    // The validator trims the name before sanitizing to a chart id; the emitter
    // must trim identically so the runtime chart id matches the id validation
    // deduplicated on (otherwise two "distinct" rules could collide at runtime).
    let cfg = config(vec![rule(
        "  padded  ",
        None,
        &[],
        RollupMetric::Bytes,
        100,
    )]);
    let engine = RollupEngine::from_config(&cfg).unwrap();
    let now = UNIX_EPOCH + Duration::from_secs(100);

    engine.observe(&record("IR", 10, 1));
    let snapshot = engine.snapshot();
    let mut emitter = RollupEmitter::new(&snapshot, Duration::from_secs(1));
    let out = emitter.render(&snapshot, now);

    assert!(out.contains("CHART netflow.rollup_padded "));
    assert!(out.contains("'netflow.rollup_padded'"));
    assert!(!out.contains("rollup___padded"));
}

#[test]
fn sanitize_id_replaces_unsafe_characters() {
    assert_eq!(sanitize_id("US"), "US");
    assert_eq!(sanitize_id("hi there/x"), "hi_there_x");
    assert_eq!(sanitize_id(""), "unknown");
    assert_eq!(sanitize_id("__overflow__"), "__overflow__");
}

#[test]
fn escape_protocol_text_neutralizes_quotes_and_control_chars() {
    assert_eq!(escape_protocol_text("Acme Corp"), "Acme Corp");
    assert_eq!(escape_protocol_text("O'Brien"), "O Brien");
    assert_eq!(escape_protocol_text("bad\nline"), "bad line");
    assert_eq!(escape_protocol_text("  spaced  "), "spaced");
    assert_eq!(escape_protocol_text("'\n\t"), "unknown");
}

#[test]
fn emitter_escapes_untrusted_dimension_labels() {
    let cfg = config(vec![rule(
        "as_names",
        Some("SRC_AS_NAME"),
        &[],
        RollupMetric::Bytes,
        100,
    )]);
    let engine = RollupEngine::from_config(&cfg).unwrap();
    let now = UNIX_EPOCH + Duration::from_secs(100);

    let mut malicious = record("", 100, 1);
    malicious.src_as_name = "A'B\nC".to_string();
    engine.observe(&malicious);

    let snapshot = engine.snapshot();
    let mut emitter = RollupEmitter::new(&snapshot, Duration::from_secs(1));
    let out = emitter.render(&snapshot, now);

    // The quote and newline from the untrusted label must not survive into the
    // stream, and the emitted line stays well-formed with a sanitized id.
    assert!(!out.contains("A'B"));
    assert!(!out.contains("B\nC"));
    assert!(out.contains("DIMENSION A_B_C 'A B C' incremental 1 1\n"));
    assert!(out.contains("SET A_B_C = 100\n"));
}

#[test]
fn emitter_merges_group_values_colliding_on_sanitized_id() {
    // Two distinct free-text group values that sanitize to the same dimension id
    // must not overwrite each other: their counters are summed into one line so
    // no group's traffic is silently dropped from the chart.
    let cfg = config(vec![rule(
        "as_names",
        Some("SRC_AS_NAME"),
        &[],
        RollupMetric::Bytes,
        100,
    )]);
    let engine = RollupEngine::from_config(&cfg).unwrap();
    let now = UNIX_EPOCH + Duration::from_secs(100);

    let mut a = record("", 30, 1);
    a.src_as_name = "AS-Foo/Bar".to_string();
    engine.observe(&a);
    let mut b = record("", 70, 1);
    b.src_as_name = "AS-Foo Bar".to_string();
    engine.observe(&b);

    let snapshot = engine.snapshot();
    let mut emitter = RollupEmitter::new(&snapshot, Duration::from_secs(1));
    let out = emitter.render(&snapshot, now);

    // Both raw labels sanitize to "AS-Foo_Bar"; exactly one dimension and one
    // SET line carrying the summed counter (30 + 70) must be emitted.
    assert_eq!(out.matches("DIMENSION AS-Foo_Bar ").count(), 1);
    assert_eq!(out.matches("SET AS-Foo_Bar = ").count(), 1);
    assert!(out.contains("SET AS-Foo_Bar = 100\n"));
}

#[test]
fn metric_units_match_rate_semantics() {
    assert_eq!(RollupMetric::Bytes.units(), "bytes/s");
    assert_eq!(RollupMetric::Packets.units(), "packets/s");
    assert_eq!(RollupMetric::Flows.units(), "flows/s");
}

#[test]
fn clamp_to_i64_saturates() {
    assert_eq!(clamp_to_i64(10), 10);
    assert_eq!(clamp_to_i64(u64::MAX), i64::MAX);
}
