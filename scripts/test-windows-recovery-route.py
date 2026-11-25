#!/usr/bin/env python3
"""Bounded workflow shape checks, not GitHub runner acceptance."""
import pathlib
import unittest
import yaml
ROOT=pathlib.Path(__file__).resolve().parent.parent
class Route(unittest.TestCase):
 def test_windows_only_shape(self):
  path=ROOT/'.github/workflows/windows-installer.yml'
  workflow=yaml.safe_load(path.read_text())
  on=workflow.get('on',workflow.get(True))
  self.assertFalse(on['workflow_dispatch']['inputs']['windows_recovery_review']['default'])
  self.assertEqual(workflow['permissions'],{'contents':'read'})
  selected=[]
  for name,job in workflow['jobs'].items():
   if name.startswith('windows-'):
    selected.append(name);self.assertEqual(job['runs-on'],'windows-2025')
    self.assertIn("github.event_name == 'workflow_dispatch' && inputs.windows_recovery_review",job['if'])
    self.assertIn('github.event.repository.private == false',job['if'])
    self.assertLessEqual(job['timeout-minutes'],25)
    for step in job['steps']:
     self.assertNotIn('upload-artifact',step.get('uses',''));self.assertNotIn('cache@',step.get('uses',''))
     if 'checkout@'in step.get('uses',''):self.assertIs(step['with']['persist-credentials'],False)
     if 'setup-go@'in step.get('uses',''):self.assertIs(step['with']['cache'],False)
     self.assertNotIn('secrets.',str(step))
   else:self.assertEqual(job['if'],'${{ !inputs.windows_recovery_review }}')
  self.assertEqual(set(selected),{'windows-contracts-and-pixels','windows-historical-current-service'})
 def test_exact_history_and_no_ui_recovery_claim(self):
  native=(ROOT/'scripts/test-edge-native-windows.ps1').read_text()
  self.assertIn("$historicalSource='bfe5d89b46324a566fd089b6c83ab9c111dc9231'",native)
  self.assertIn('WINDOWS_HISTORICAL_CURRENT=',native)
  self.assertIn('-ResumeInstall',native);self.assertIn('-RecoverUpgrade',native)
  self.assertIn('fixture actual service creation acknowledgement loss',native)
  self.assertIn('First-install acknowledgement loss lost pending state',native)
  self.assertIn('Actual first-install review lost exact stopped state',native)
  self.assertIn('Pending first-install rerun was not refused',native)
  self.assertIn('Explicit native install-only resume did not settle',native)
  self.assertNotIn("Button 'Resume install",(ROOT/'edge/packaging/setup-wizard.ps1').read_text())
if __name__=='__main__':unittest.main()
