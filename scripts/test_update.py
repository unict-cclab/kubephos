"""Update regressions with a fake Docker CLI; never changes running services."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]

class UpdateTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.base = Path(self.temporary.name)
        self.installation = self.base / 'installed with spaces'
        result = subprocess.run(['bash',str(ROOT/'install.sh'),'--prepare-only','--directory',str(self.installation),'--admin-email','admin@example.test'],capture_output=True,text=True)
        self.assertEqual(result.returncode,0,result.stderr)
        tools=self.base/'bin';tools.mkdir()
        self.log=self.base/'docker.log'
        (tools/'docker').write_text('''#!/usr/bin/env bash
set -eu
printf '%s\\n' "$*" >> "$UPDATE_TEST_LOG"
printf '%s\\n' 'compose output hidden' >&2
if [[ "$*" == *'psql '* ]]; then printf '%s\\n' "${UPDATE_TEST_ACTIVE:-0}"; fi
if [[ "$*" == *' auth-sync' && "${UPDATE_TEST_FAIL:-}" == yes ]]; then exit 1; fi
''')
        (tools/'docker').chmod(0o755)
        self.env={**os.environ,'PATH':str(tools)+os.pathsep+os.environ['PATH'],'UPDATE_TEST_LOG':str(self.log)}

    def test_installed_updater_works_from_another_directory_and_preserves_credentials(self):
        original=(self.installation/'credentials.json').read_bytes()
        env_file=self.installation/'.env'
        env_file.write_text(env_file.read_text().replace('http://localhost:8080','https://new.example.test'))
        result=subprocess.run([str(self.installation/'update.sh'),'--no-pull'],cwd=self.base,env=self.env,capture_output=True,text=True)
        self.assertEqual(result.returncode,0,result.stderr)
        calls=self.log.read_text().splitlines()
        self.assertTrue(all(str(env_file) in call for call in calls))
        sync=next(i for i,c in enumerate(calls) if c.endswith('auth-sync'))
        start=next(i for i,c in enumerate(calls) if c.endswith('up -d --wait --wait-timeout 180'))
        self.assertLess(sync,start)
        self.assertEqual(original,(self.installation/'credentials.json').read_bytes())
        self.assertNotIn('compose output hidden',result.stdout+result.stderr)
        self.assertIn('compose output hidden',(self.installation/'update.log').read_text())
        self.assertFalse((self.installation/'.install-lock').exists())

    def test_source_updater_accepts_an_explicit_installation(self):
        result=subprocess.run([str(ROOT/'update.sh'),'--directory',str(self.installation)],cwd=self.base,env=self.env,capture_output=True,text=True)
        self.assertEqual(result.returncode,0,result.stderr)
        self.assertIn(' pull',self.log.read_text())
        self.assertTrue((self.installation/'compose.previous.yaml').exists())

    def test_active_operations_prevent_restarts(self):
        result=subprocess.run([str(self.installation/'update.sh'),'--no-pull'],env={**self.env,'UPDATE_TEST_ACTIVE':'1'},capture_output=True,text=True)
        self.assertNotEqual(result.returncode,0)
        self.assertNotIn(' stop ',self.log.read_text())

    def test_sync_failure_keeps_application_stopped_and_reports_only_the_log_path(self):
        result=subprocess.run([str(self.installation/'update.sh'),'--no-pull'],env={**self.env,'UPDATE_TEST_FAIL':'yes'},capture_output=True,text=True)
        self.assertNotEqual(result.returncode,0)
        self.assertIn('update.log',result.stderr)
        self.assertNotIn('compose output hidden',result.stdout+result.stderr)
        self.assertNotIn('up -d --wait --wait-timeout 180',self.log.read_text())

if __name__=='__main__':unittest.main()
