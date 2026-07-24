#!/usr/bin/env python3

import sys
import unittest
from pathlib import Path

INTEGRATIONS_DIR = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(INTEGRATIONS_DIR))

import gen_prometheus_profiles as gpp
from _common import COLLECTOR_VALIDATOR, load_yaml


def sample_profile():
    return {
        'match': 'demo_*',
        'app': 'demo',
        'meta': {
            'name': 'Demo Exporter',
            'link': 'https://example.com',
            'icon_filename': 'demo.svg',
            'categories': ['data-collection.web-servers-and-proxies'],
            'keywords': ['demo', 'prometheus'],
            'description': 'Monitor Demo.',
        },
        'template': {
            'family': 'Demo',
            'context_namespace': 'demo',
            'groups': [
                {
                    'family': 'Process',
                    'metrics': ['demo_up'],
                    'charts': [
                        {
                            'title': 'Uptime',
                            'context': 'uptime',
                            'units': 'seconds',
                            'dimensions': [{'selector': 'demo_up', 'name': 'uptime'}],
                        },
                    ],
                },
                {
                    'family': 'Frontends',
                    'chart_defaults': {'instances': {'by_labels': ['proxy']}},
                    'groups': [
                        {
                            'family': 'Status',
                            'metrics': ['demo_frontend_status'],
                            'charts': [
                                {
                                    'title': 'Frontend Status',
                                    'context': 'frontend_status',
                                    'units': 'status',
                                    'type': 'stacked',
                                    'dimensions': [
                                        {'selector': 'demo_frontend_status', 'name_from_label': 'state'},
                                    ],
                                },
                            ],
                        },
                    ],
                },
            ],
        },
    }


class BuildScopesTest(unittest.TestCase):
    def setUp(self):
        self.scopes = gpp.build_scopes(sample_profile(), 'demo')

    def test_one_scope_per_top_level_group(self):
        self.assertEqual([s['name'] for s in self.scopes], ['process', 'frontends'])

    def test_global_scope_has_no_labels(self):
        process = self.scopes[0]
        self.assertEqual(process['labels'], [])

    def test_scope_labels_come_from_instances_by_labels(self):
        frontends = self.scopes[1]
        self.assertEqual([label['name'] for label in frontends['labels']], ['proxy'])

    def test_context_is_expanded_with_prometheus_and_app(self):
        # context_namespace == app, so the app segment is not repeated.
        metric = self.scopes[0]['metrics'][0]
        self.assertEqual(metric['name'], 'prometheus.demo.uptime')

    def test_static_dimension_name(self):
        metric = self.scopes[0]['metrics'][0]
        self.assertEqual(metric['dimensions'], [{'name': 'uptime'}])

    def test_name_from_label_dimension(self):
        metric = self.scopes[1]['metrics'][0]
        self.assertEqual(metric['dimensions'], [{'name': 'a dimension per state'}])

    def test_chart_type_is_preserved(self):
        self.assertEqual(self.scopes[1]['metrics'][0]['chart_type'], 'stacked')

    def test_chart_type_defaults_to_line(self):
        self.assertEqual(self.scopes[0]['metrics'][0]['chart_type'], 'line')


class ContextCompositionTest(unittest.TestCase):
    def test_root_namespace_kept_when_different_from_app(self):
        profile = sample_profile()
        profile['app'] = 'srv'
        scopes = gpp.build_scopes(profile, 'demo')
        self.assertEqual(scopes[0]['metrics'][0]['name'], 'prometheus.srv.demo.uptime')

    def test_app_defaults_to_profile_name(self):
        profile = sample_profile()
        del profile['app']
        # No app + root namespace "demo" -> prometheus.demo.<context>.
        scopes = gpp.build_scopes(profile, 'demo')
        self.assertEqual(scopes[0]['metrics'][0]['name'], 'prometheus.demo.uptime')


class BuildProfileModuleTest(unittest.TestCase):
    def base_module(self):
        data = load_yaml(gpp.PROMETHEUS_METADATA_PATH)
        return data['modules'][0]

    def test_module_validates_against_collector_schema(self):
        module = gpp.build_profile_module(self.base_module(), sample_profile(), 'demo')
        self.assertIsNotNone(module)
        # A full collector document with a single generated module must validate.
        doc = {'plugin_name': 'go.d.plugin', 'modules': [module]}
        COLLECTOR_VALIDATOR.validate(doc)

    def test_module_identity_and_presentation(self):
        module = gpp.build_profile_module(self.base_module(), sample_profile(), 'demo')
        self.assertEqual(module['meta']['id'], 'collector-go.d.plugin-prometheus-demo')
        self.assertTrue(module['meta']['community'])
        self.assertEqual(module['meta']['monitored_instance']['name'], 'Demo Exporter')
        self.assertEqual(module['meta']['monitored_instance']['icon_filename'], 'demo.svg')

    def test_missing_meta_block_skips_page(self):
        profile = sample_profile()
        del profile['meta']
        self.assertIsNone(gpp.build_profile_module(self.base_module(), profile, 'demo'))

    def test_incomplete_meta_block_skips_page(self):
        profile = sample_profile()
        del profile['meta']['icon_filename']
        self.assertIsNone(gpp.build_profile_module(self.base_module(), profile, 'demo'))


class StockHaproxyProfileTest(unittest.TestCase):
    """The shipped haproxy profile must produce a valid, non-empty page."""

    def test_haproxy_profile_generates_valid_module(self):
        path = gpp.PROFILES_DIR / 'haproxy.yaml'
        if not path.is_file():
            self.skipTest('haproxy profile not present')
        data = load_yaml(gpp.PROMETHEUS_METADATA_PATH)
        module = gpp.build_profile_module(data['modules'][0], load_yaml(path), 'haproxy')
        self.assertIsNotNone(module)
        self.assertEqual(module['meta']['id'], 'collector-go.d.plugin-prometheus-haproxy')

        scopes = module['metrics']['scopes']
        total_metrics = sum(len(scope['metrics']) for scope in scopes)
        self.assertEqual(total_metrics, 92)
        for scope in scopes:
            for metric in scope['metrics']:
                self.assertTrue(metric['name'].startswith('prometheus.haproxy.'))

        COLLECTOR_VALIDATOR.validate({'plugin_name': 'go.d.plugin', 'modules': [module]})


if __name__ == '__main__':
    unittest.main()
