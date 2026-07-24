#!/usr/bin/env python3
"""Generate integration catalog entries for Prometheus chart profiles.

Prometheus chart profiles
(``src/go/plugin/go.d/config/go.d/prometheus.profiles/``) declare, per exporter,
the curated charts the generic Prometheus collector renders. Each profile is the
single source of truth for one integration page:

* its optional ``meta:`` block supplies the catalog presentation metadata (name,
  link, icon, categories, keywords, description); and
* its ``template:`` (a chart template — see
  ``src/go/plugin/framework/charttpl/README.md``) declares every chart's context,
  title, units, type, and dimensions.

This module turns a profile into a ``metadata.yaml``-shaped collector module
entry whose ``metrics.scopes`` mirror the profile's chart tree. It follows the
``azure_monitor`` "one collector -> many pages" model (a per-component
``<<: *module`` entry with its own ``monitored_instance`` and ``metrics.scopes``)
but the entry is GENERATED from the profile instead of hand-written, so the
profile YAML stays the single source of truth.

``gen_integrations.py`` picks these entries up transparently: ``load_collectors``
appends ``generate_profile_modules()`` output to the Prometheus collector's
modules, so every profile automatically yields its own integration page (and its
row in ``COLLECTORS.md``) with no per-exporter hand-editing.

Scope model
-----------
Each top-level group under ``template.groups`` becomes one scope (the operator
menu the profile author already curated). A scope's labels are the instance
identity (``instances.by_labels``) shared by its charts; a scope's metrics are
every chart in that group subtree, with the chart context expanded to the
runtime context (``prometheus.<app>.<...>.<chart context>``).
"""

import re
from copy import deepcopy

from _common import REPO_PATH, debug, load_yaml, warn

PROMETHEUS_DIR = REPO_PATH / 'src' / 'go' / 'plugin' / 'go.d' / 'collector' / 'prometheus'
PROMETHEUS_METADATA_PATH = PROMETHEUS_DIR / 'metadata.yaml'
PROFILES_DIR = (
    REPO_PATH / 'src' / 'go' / 'plugin' / 'go.d' / 'config' / 'go.d' / 'prometheus.profiles' / 'default'
)

# The generic Prometheus collector always emits contexts under this root; the
# per-job "app" segment is appended after it (see the collector's
# newAutogenSpec / buildMergedChartTemplate).
CONTEXT_ROOT = 'prometheus'

CHART_TYPES = {'line', 'area', 'stacked', 'heatmap'}
DEFAULT_CHART_TYPE = 'line'

# Presentation fields a profile's `meta:` block must provide for a valid
# monitored_instance (see integrations/schemas/shared.json#/$defs/instance).
REQUIRED_META_FIELDS = ('name', 'link', 'icon_filename', 'categories')


def is_prometheus_metadata(path):
    """Return True if path is the Prometheus collector metadata.yaml."""
    try:
        return path.resolve() == PROMETHEUS_METADATA_PATH.resolve()
    except OSError:
        return path == PROMETHEUS_METADATA_PATH


def _slug(text):
    """Lowercase and reduce to [a-z0-9_], collapsing runs of separators."""
    slug = re.sub(r'[^a-z0-9]+', '_', str(text).lower())
    return slug.strip('_')


def iter_profile_files():
    """Yield profile YAML paths in deterministic (sorted) order."""
    if not PROFILES_DIR.is_dir():
        return []
    return sorted(PROFILES_DIR.glob('*.yaml'))


def _resolve_app(profile, profile_name):
    """The chart-context "app" segment.

    Mirrors the collector's precedence at generation time: an explicit profile
    ``app`` wins, otherwise the profile basename is the fallback (the runtime's
    last resort is the job name, which is not known here).
    """
    return profile.get('app') or profile_name


def _base_context_parts(app, root_namespace):
    """Context parts shared by every chart: prometheus[.app][.root_namespace].

    The collector collapses the profile's root ``context_namespace`` when it
    equals ``app`` (so the app segment is not repeated); this reproduces that.
    """
    parts = [CONTEXT_ROOT]
    if app:
        parts.append(app)
    if root_namespace and root_namespace != app:
        parts.append(root_namespace)
    return parts


def _by_labels(node):
    """Return instances.by_labels declared on a group/chart node, or None."""
    instances = (node or {}).get('instances')
    if isinstance(instances, dict) and instances.get('by_labels'):
        return list(instances['by_labels'])
    return None


def _walk_charts(group, ctx_parts, inherited_by_labels):
    """Yield (chart, full_context, by_labels) for every chart in the subtree.

    Context namespaces compose down the tree (joined with '.'); instance
    identity (``by_labels``) is inherited from the nearest ``chart_defaults``
    and can be overridden per chart.
    """
    namespace = group.get('context_namespace')
    parts = ctx_parts + [namespace] if namespace else ctx_parts

    defaults_by_labels = _by_labels(group.get('chart_defaults'))
    group_by_labels = defaults_by_labels if defaults_by_labels is not None else inherited_by_labels

    for chart in group.get('charts', []) or []:
        chart_by_labels = _by_labels(chart)
        if chart_by_labels is None:
            chart_by_labels = group_by_labels
        context = '.'.join(parts + [chart['context']])
        yield chart, context, (chart_by_labels or [])

    for nested in group.get('groups', []) or []:
        yield from _walk_charts(nested, parts, group_by_labels)


def _dimension_names(chart):
    """Metadata dimension entries for a chart.

    A static ``name`` maps directly; a ``name_from_label`` produces one
    dimension per distinct label value at runtime, described with the catalog's
    established "a dimension per <label>" convention; anything else is inferred
    by the engine (histogram ``le``, summary ``quantile``, stateset value).
    """
    names = []
    for dim in chart.get('dimensions', []) or []:
        if dim.get('name'):
            name = dim['name']
        elif dim.get('name_from_label'):
            name = f"a dimension per {dim['name_from_label']}"
        else:
            name = 'a dimension per value'
        if name not in names:
            names.append(name)
    return [{'name': name} for name in names]


def _metric_from_chart(chart, context):
    chart_type = chart.get('type', DEFAULT_CHART_TYPE)
    if chart_type not in CHART_TYPES:
        chart_type = DEFAULT_CHART_TYPE
    return {
        'name': context,
        'description': chart.get('title', ''),
        'unit': chart.get('units', ''),
        'chart_type': chart_type,
        'dimensions': _dimension_names(chart),
    }


def _scope_from_group(top_group, base_parts, display_name):
    """Build one metrics scope from a top-level profile group."""
    charts = list(_walk_charts(top_group, base_parts, None))

    label_sets = {tuple(by_labels) for _, _, by_labels in charts}
    if len(label_sets) == 1:
        scope_labels = list(next(iter(label_sets)))
    else:
        scope_labels = _by_labels(top_group.get('chart_defaults')) or []

    family = top_group.get('family', '')
    if scope_labels:
        description = f'These metrics refer to each {display_name} {family} instance.'
    else:
        description = f'These metrics refer to the entire {display_name} instance.'

    return {
        'name': _slug(family) or 'global',
        'description': description,
        'labels': [{'name': label, 'description': f'The "{label}" label value.'} for label in scope_labels],
        'metrics': [_metric_from_chart(chart, context) for chart, context, _ in charts],
    }


def build_scopes(profile, profile_name):
    """Emit the ``metrics.scopes`` list for a profile."""
    template = profile.get('template') or {}
    app = _resolve_app(profile, profile_name)
    base_parts = _base_context_parts(app, template.get('context_namespace'))
    display_name = (profile.get('meta') or {}).get('name') or template.get('family') or app

    return [
        _scope_from_group(group, base_parts, display_name)
        for group in template.get('groups', []) or []
    ]


def build_profile_module(base_module, profile, profile_name):
    """Build a metadata.yaml collector module entry for one profile.

    Inherits the generic Prometheus module (setup, method, troubleshooting, …)
    and overrides identity, presentation, overview description, and
    ``metrics.scopes`` from the profile. Returns None when the profile lacks the
    presentation ``meta:`` block required for a catalog page.
    """
    meta_block = profile.get('meta') or {}
    missing = [f for f in REQUIRED_META_FIELDS if not meta_block.get(f)]
    if missing:
        debug(
            f'prometheus profile {profile_name!r}: skipping integration page, '
            f'meta block missing {", ".join(missing)}'
        )
        return None

    app = _resolve_app(profile, profile_name)
    display_name = meta_block['name']

    module = deepcopy(base_module)

    module['meta']['id'] = f'collector-go.d.plugin-prometheus-{_slug(app)}'
    module['meta']['community'] = True
    module['meta']['monitored_instance'] = {
        'name': display_name,
        'link': meta_block['link'],
        'icon_filename': meta_block['icon_filename'],
        'categories': list(meta_block['categories']),
    }
    module['meta']['keywords'] = list(meta_block.get('keywords', []))

    description = meta_block.get('description') or f'Monitor {display_name}.'
    module['overview']['data_collection']['metrics_description'] = description

    metrics = module.setdefault('metrics', {})
    metrics['folding'] = {'title': 'Metrics', 'enabled': False}
    metrics['description'] = (
        f'These are the curated charts the profile '
        f'`{PROFILES_DIR.name}/{profile_name}.yaml` renders from the '
        f'{display_name} exporter.'
    )
    metrics['availability'] = []
    metrics['scopes'] = build_scopes(profile, profile_name)

    return module


def generate_profile_modules(base_module):
    """Return generated collector module entries for every Prometheus profile.

    ``base_module`` is the generic Prometheus module (``modules[0]``), used as
    the inheritance base so profile pages share the collector's setup/method
    prose, exactly like a hand-written ``<<: *module`` entry would.
    """
    modules = []
    for path in iter_profile_files():
        profile = load_yaml(path)
        if not profile:
            continue
        profile_name = path.stem
        try:
            module = build_profile_module(base_module, profile, profile_name)
        except (KeyError, TypeError) as e:
            warn(f'failed to generate integration entry from prometheus profile: {e}', path)
            continue
        if module is not None:
            modules.append(module)
    return modules
