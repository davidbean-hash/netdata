use super::super::PluginConfig;
use crate::flow::canonical_flow_field_names;
use anyhow::{Result, bail};
use std::collections::HashSet;

pub(super) fn validate_rollups(cfg: &PluginConfig) -> Result<()> {
    let canonical: HashSet<&'static str> = canonical_flow_field_names().collect();
    let mut seen_names: HashSet<&str> = HashSet::new();

    for rule in &cfg.rollups.rules {
        let name = rule.name.trim();
        if name.is_empty() {
            bail!("rollups.rules: every rule must have a non-empty name");
        }
        if !seen_names.insert(name) {
            bail!("rollups.rules: duplicate rule name '{name}'");
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
