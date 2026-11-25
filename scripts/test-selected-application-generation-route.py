#!/usr/bin/env python3
"""Selected-generation native route/guard contracts, not Docker acceptance."""
import ast
import os
import pathlib
import subprocess
import sys
import unittest
HERE = pathlib.Path(__file__).resolve().parent

class Tests(unittest.TestCase):
    def test_refuses_nonrunner_before_build_or_docker(self):
        env = dict(os.environ, HEXTHINGS_SELECTED_GENERATION_ONLY='1')
        env.pop('GITHUB_ACTIONS', None)
        result = subprocess.run([sys.executable, str(HERE / 'test-secure-application-native.py')], env=env, capture_output=True, text=True, timeout=10)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Disposable standard Ubuntu runner only', result.stderr)

    def test_generation_route_uses_historical_build_and_only_actual_provider_proof(self):
        source = (HERE / 'test-secure-application-native.py').read_text()
        self.assertIn("OLD_COMMIT='bfe5d89b46324a566fd089b6c83ab9c111dc9231'", source)
        tree = ast.parse(source)
        main = next(node for node in tree.body if isinstance(node, ast.FunctionDef) and node.name == 'main')
        branch = next(node for node in ast.walk(main) if isinstance(node, ast.If) and isinstance(node.test, ast.Name) and node.test.id == 'generation_only')
        self.assertIsInstance(branch.body[-1], ast.Return)
        self.assertEqual(len(branch.body), 2)
        text = ast.unparse(branch)
        self.assertIn("load('test-secure-application-provider-native').prove", text)
        self.assertNotIn('preparation_only=True', text)
        self.assertNotIn('same', text)
        self.assertIn('if preparation_only or generation_only:FIXTURE_JWT=', source)

    def test_provider_updates_preserve_state_directory_and_prepare_new_immutable_plan(self):
        source = (HERE / 'test-secure-application-provider-native.py').read_text()
        self.assertNotIn('os.rename', source)
        self.assertNotIn('shutil.rmtree', source)
        self.assertIn("m.application.prepare(root,project,image,root/'second-plan')", source)
        self.assertIn("stat().st_ino!=state_inode", source)
        self.assertIn('len(archives)!=2', source)
        self.assertIn('Initial settled selector is fixture-created, not installer proof.', source)
        self.assertIn("evidence=list(provider.journal.resource_directory.glob", source)

    def test_default_off_public_standard_readonly_no_artifact_storage(self):
        source = (HERE.parent / '.github/workflows/ci.yml').read_text()
        inputs = source.split('      selected_application_generation:\n')[1].split('      real_application_preparation:')[0]
        self.assertIn('default: false', inputs)
        job = source.split('\n  selected-application-generation:\n')[1]
        for value in ['inputs.selected_application_generation', 'github.event.repository.private == false',
                      'runs-on: ubuntu-latest', 'contents: read', 'persist-credentials: false',
                      'cache: false', 'HEXTHINGS_SELECTED_GENERATION_ONLY=1']:
            self.assertIn(value, job)
        self.assertNotIn('upload-artifact', job)
        self.assertNotIn('contents: write', job)

if __name__ == '__main__':
    unittest.main()
