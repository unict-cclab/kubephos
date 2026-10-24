"""Exercise the release gate without contacting GitHub or publishing anything."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


class ReleaseChecksTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        directory = Path(self.temporary.name)
        self.log = directory / 'calls.jsonl'
        self.env = {**os.environ, 'PATH': f'{directory}:{os.environ["PATH"]}',
                    'GH_TOKEN': 'test-token', 'GITHUB_REPOSITORY': 'example/project',
                    'GITHUB_SHA': 'a' * 40, 'KUBEPHOS_GATE_LOG': str(self.log)}
        (directory / 'gh').write_text('''#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
log = Path(os.environ['KUBEPHOS_GATE_LOG'])
calls = [json.loads(line) for line in log.read_text().splitlines()] if log.exists() else []
with log.open('a') as output: output.write(json.dumps(sys.argv[1:]) + '\\n')
if sys.argv[1:3] == ['run', 'list']:
    if os.environ.get('KUBEPHOS_GATE_API_ERROR'): sys.exit(1)
    attempts = sum(call[:2] == ['run', 'list'] for call in calls)
    if attempts >= int(os.environ.get('KUBEPHOS_GATE_MISSING', '0')): print('1234')
elif sys.argv[1:3] == ['run', 'watch']:
    sys.exit(0 if os.environ.get('KUBEPHOS_GATE_CONCLUSION', 'success') == 'success' else 1)
else: sys.exit(2)
''')
        (directory / 'sleep').write_text('#!/bin/sh\nexit 0\n')
        for name in ('gh', 'sleep'):
            (directory / name).chmod(0o755)

    def run_gate(self, **overrides):
        result = subprocess.run(['bash', str(ROOT / 'scripts/verify_release_checks.sh')],
                                env={**self.env, **overrides}, capture_output=True, text=True)
        calls = [json.loads(line) for line in self.log.read_text().splitlines()]
        return result, calls

    def test_reuses_checks_for_exact_push_commit(self):
        result, calls = self.run_gate()
        self.assertEqual(result.returncode, 0, result.stderr)
        listing, watch = calls
        self.assertEqual(listing[listing.index('--commit') + 1], 'a' * 40)
        self.assertEqual(listing[listing.index('--event') + 1], 'push')
        self.assertEqual(listing[listing.index('--workflow') + 1], 'checks.yml')
        self.assertEqual(watch[:3], ['run', 'watch', '1234'])
        self.assertIn('--exit-status', watch)
        self.assertEqual(watch[watch.index('--repo') + 1], 'example/project')

    def test_failed_cancelled_or_incomplete_checks_block_release(self):
        for conclusion in ('failure', 'cancelled', 'timed_out', 'skipped'):
            with self.subTest(conclusion=conclusion):
                self.log.unlink(missing_ok=True)
                result, _ = self.run_gate(KUBEPHOS_GATE_CONCLUSION=conclusion)
                self.assertNotEqual(result.returncode, 0)

    def test_waits_for_branch_push_run_to_be_visible(self):
        result, calls = self.run_gate(KUBEPHOS_GATE_MISSING='2')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(sum(call[:2] == ['run', 'list'] for call in calls), 3)

    def test_missing_checks_block_release_without_starting_duplicate_tests(self):
        result, calls = self.run_gate(KUBEPHOS_GATE_MISSING='99')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Push the commit to a branch', result.stderr)
        self.assertEqual(len(calls), 4)
        self.assertTrue(all(call[:2] == ['run', 'list'] for call in calls))

    def test_api_failure_blocks_release(self):
        result, calls = self.run_gate(KUBEPHOS_GATE_API_ERROR='1')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(len(calls), 1)


if __name__ == '__main__':
    unittest.main()
