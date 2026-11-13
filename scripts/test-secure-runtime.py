#!/usr/bin/env python3
import copy
import importlib.util
import pathlib
import unittest

spec = importlib.util.spec_from_file_location('runtime', pathlib.Path(__file__).with_name('secure-runtime.py'))
runtime = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runtime)


class Tests(unittest.TestCase):
    def configs(self):
        old = {'services': {name: {'image': 'mutable:tag', 'build': {'context': '/source'}}
                            for name in ['api', 'ingest', 'web', 'mosquitto', 'postgres', 'mcp']}}
        old['services']['ai-runtime'] = {'profiles': ['ai'], 'build': {'context': '/ai'}}
        old['services']['postgres']['volumes'] = [{'type': 'bind', 'source': '/verified/database', 'target': '/data'}]
        new = copy.deepcopy(old)
        new['services']['secure-web'] = {'image': 'nginx:mutable', 'network_mode': 'service:api', 'depends_on': {'api': {}, 'web': {}}}
        images = {name: 'sha256:' + 'a' * 64 for name in old['services'] if name != 'ai-runtime'}
        proposed = dict(images, mosquitto='sha256:' + 'b' * 64, **{'secure-web': 'sha256:' + 'c' * 64})
        return old, new, images, proposed

    def test_freezes_exact_images_and_absolute_mount_without_mutating_inputs(self):
        old, new, images, proposed = self.configs()
        original = copy.deepcopy((old, new))
        result = runtime.pair(old, new, images, proposed)
        for config in [result['previous'], result['candidate']]:
            self.assertNotIn('ai-runtime', config['services'])
            for service in config['services'].values():
                self.assertNotIn('build', service)
                self.assertEqual(service['pull_policy'], 'never')
                self.assertRegex(service['image'], '^sha256:[a-f0-9]{64}$')
        self.assertEqual(result['disabled_profiles'], ['ai-runtime'])
        self.assertEqual((old, new), original)
        self.assertEqual(result['previous']['services']['postgres']['volumes'][0]['source'], '/verified/database')

    def test_cannot_smuggle_a_code_upgrade_service_or_unverified_optional_profile(self):
        for change in ['application', 'added', 'removed', 'image', 'namespace', 'dependency', 'mount', 'replica', 'unknown']:
            with self.subTest(change=change):
                old, new, images, proposed = self.configs()
                if change == 'application':
                    proposed['api'] = 'sha256:' + 'd' * 64
                if change == 'added':
                    new['services']['unreviewed'] = {}
                if change == 'removed':
                    del new['services']['mcp']
                if change == 'image':
                    proposed['secure-web'] = 'nginx:mutable'
                if change == 'namespace':
                    new['services']['secure-web']['network_mode'] = 'service:missing'
                if change == 'dependency':
                    new['services']['api']['depends_on'] = ['ai-runtime']
                if change == 'mount':
                    old['services']['postgres']['volumes'][0]['source'] = './relative'
                if change == 'replica':
                    new['services']['api']['deploy'] = {'replicas': 2}
                if change == 'unknown':
                    proposed['missing-service'] = 'sha256:' + 'e' * 64
                with self.assertRaises(ValueError):
                    runtime.pair(old, new, images, proposed)

    def test_active_optional_profile_must_keep_its_exact_existing_image(self):
        old, new, images, proposed = self.configs()
        images['ai-runtime'] = proposed['ai-runtime'] = 'sha256:' + 'e' * 64
        result = runtime.pair(old, new, images, proposed)
        self.assertEqual(result['disabled_profiles'], [])
        self.assertEqual(result['candidate']['services']['ai-runtime']['image'], images['ai-runtime'])
        del proposed['ai-runtime']
        with self.assertRaises(ValueError):
            runtime.pair(old, new, images, proposed)

    def test_missing_ordinary_service_image_is_never_treated_as_optional(self):
        old, new, images, proposed = self.configs()
        del images['mcp']
        with self.assertRaises(ValueError):
            runtime.pair(old, new, images, proposed)


if __name__ == '__main__':
    unittest.main()
