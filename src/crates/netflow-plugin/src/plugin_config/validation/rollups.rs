use super::super::PluginConfig;
use crate::aggregation::sanitize_id;
use crate::flow::canonical_flow_field_names;
use anyhow::{Result, bail};
use std::collections::HashSet;

pub(super) fn validate_rollups(cfg: &PluginConfig) -> Result<()> {
    let canonical: HashSet<&'static str> = canonical_flow_field_names().collect();
    let mut seen_names: HashSet<&str> = HashSet::new();
    let mut seen_chart_ids: HashSet<String> = HashSet::new();

    for rule in &cfg.rollups.rules {
        let name = rule.name.trim();
        if name.is_empty() {
            bail!("rollups.rules: every rule must have a non-empty name");
        }
        if !seen_names.insert(name) {
            bail!("rollups.rules: duplicate rule name '{name}'");
        }
        let chart_id = sanitize_id(name);
        if !seen_chart_ids.insert(chart_id.clone()) {
            bail!(
                "rollups.rules['{name}']: name collides with another rule after sanitization (chart id 'netflow.rollup_{chart_id}'); choose more distinct names"
            );
        }
        if rule.max_cardinality == 0 {
            bail!("rollups.rules['{name}']: max_cardinality must be greater than 0");
        }

        if let Some(field) = &rule.group_by
            && !canonical.contains(field.as_str())
        {
            bail!("rollups.rules['{name}']: group_by '{field}' is not a known flow field");
        }

        for field in rule.filters.keys() {
            if !canonical.contains(field.as_str()) {
                bail!("rollups.rules['{name}']: filter field '{field}' is not a known flow field");
            }
        }
    }

    Ok(())
}
