"""Image retention exercises the complete updater with a fake Docker daemon."""
import os
import subprocess
import unittest
import test_update

class UpdateCleanupTests(unittest.TestCase):
    def setUp(self):
        fixture = test_update.UpdateTests()
        fixture.setUp()
        self.addCleanup(fixture.doCleanups)
        self.fixture = fixture
        binary = fixture.base / 'bin/docker'
        binary.write_text('''#!/usr/bin/env bash
set -eu
printf '%s\n' "$*" >> "$UPDATE_TEST_LOG"
case "$*" in
 *'psql '*) printf '0\n';;
 *'config --images app'*) printf 'postgres:18\nghcr.io/unict-cclab/kubephos:v4\nliquibase/liquibase:4\n';;
 'image ls '*)
  printf '2026-11-12|sha256:active|ghcr.io/unict-cclab/kubephos:v4\n'
  printf '2026-11-11|sha256:rollback|ghcr.io/unict-cclab/kubephos:v3\n'
  printf '2026-11-10|sha256:other-install|ghcr.io/unict-cclab/kubephos:v2\n'
  printf '2026-11-09|sha256:old|ghcr.io/unict-cclab/kubephos:v1\n'
  printf '2026-11-08|sha256:foreign|unrelated/service:v1\n';;
 'ps -aq') printf 'container1\ncontainer2\n';;
 'inspect '*)
  [[ "${INSPECT_FAIL:-}" != yes ]] || exit 1
  printf 'sha256:active\nsha256:other-install\n';;
esac
''')
        binary.chmod(0o755)

    def run_update(self, **extra):
        fixture = self.fixture
        result = subprocess.run([str(fixture.installation/'update.sh'),'--no-pull'],
            env={**fixture.env, **extra}, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        return fixture.log.read_text().splitlines()

    def test_removes_only_old_unreferenced_release_and_retains_rollback(self):
        calls = self.run_update()
        removed = [c for c in calls if c.startswith('image rm')]
        self.assertTrue(removed)
        self.assertTrue(all(c == 'image rm ghcr.io/unict-cclab/kubephos:v1' for c in removed))
        self.assertFalse(any('prune' in c or 'volume rm' in c or '--force' in c for c in calls))

    def test_failed_reference_inventory_never_deletes_images(self):
        calls = self.run_update(INSPECT_FAIL='yes')
        self.assertFalse(any(c.startswith('image rm') for c in calls))
