"""Installer regression checks; uses temporary directories and never starts Docker."""
import json
import os
from pathlib import Path
import stat
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


class InstallerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='kubephos-installer-')
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name) / 'installation with spaces'

    def run_installer(self, *args, success=True, env=None):
        result = subprocess.run(['bash', str(ROOT / 'install.sh'), '--directory', str(self.directory), '--admin-email', 'admin@example.test', *args], text=True, capture_output=True, env=env)
        self.assertEqual(result.returncode == 0, success, result.stdout + result.stderr)
        return result

    def test_generated_secrets_and_resumable_preparation(self):
        result = self.run_installer('--prepare-only', '--url', 'http://example.test:8091', '--version', 'v0.9.0-rc.1')
        values = dict(line.split('=', 1) for line in (self.directory / '.env').read_text().splitlines() if line and not line.startswith('#'))
        secrets = [value for key, value in values.items() if key.endswith(('PASSWORD', 'SECRET'))]
        self.assertEqual(len(set(secrets)), 4)
        self.assertTrue(all(len(secret) == 64 for secret in secrets))
        self.assertEqual(values['KUBEPHOS_PORT'], '8091')
        self.assertEqual(values['KUBEPHOS_BIND_ADDRESS'], '0.0.0.0')
        self.assertTrue(values['COMPOSE_PROJECT_NAME'].startswith('kubephos-'))
        account = json.loads((self.directory / 'credentials.json').read_text())
        self.assertEqual(account['username'], 'admin')
        self.assertEqual(len(account['password']), 48)
        for name in ('.env', 'credentials.json'):
            self.assertEqual(stat.S_IMODE((self.directory / name).stat().st_mode), 0o600)
            self.assertNotIn(account['password'], result.stdout + result.stderr)
        original = (self.directory / '.env').read_bytes()
        self.run_installer('--prepare-only', '--url', 'http://example.test:8091', '--version', 'v0.9.0-rc.1')
        result = subprocess.run(['bash', str(ROOT / 'install.sh'), '--directory', str(self.directory), '--prepare-only'], text=True, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(original, (self.directory / '.env').read_bytes())
        self.run_installer('--prepare-only', '--version', 'v1.0.0', success=False)
        self.assertEqual(original, (self.directory / '.env').read_bytes())

    def test_existing_installation_is_not_adopted_or_overwritten(self):
        self.directory.mkdir()
        (self.directory / '.env').write_text('POSTGRES_PASSWORD=existing\n')
        self.run_installer('--prepare-only', success=False)
        self.assertEqual((self.directory / '.env').read_text(), 'POSTGRES_PASSWORD=existing\n')
        self.assertFalse((self.directory / 'credentials.json').exists())

    def test_rejects_invalid_options_without_creating_files(self):
        for args in (('--url', 'http://host/invalid'), ('--url', 'http://host:99999'), ('--url', 'http://host:0'), ('--url', 'http://user:password@host'), ('--version', 'tag$(false)'), ('--unknown',)):
            with self.subTest(args=args):
                self.run_installer('--prepare-only', *args, success=False)
                self.assertFalse((self.directory / '.env').exists())

    def test_https_stays_behind_loopback_proxy(self):
        self.run_installer('--prepare-only', '--url', 'https://kubephos.example.test')
        config = (self.directory / '.env').read_text()
        self.assertIn('KUBEPHOS_PORT=8080\n', config)
        self.assertIn('KUBEPHOS_BIND_ADDRESS=127.0.0.1\n', config)

    def test_binding_uses_the_actual_host_not_a_hostname_prefix(self):
        for host, expected in (('localhost.example.test', '0.0.0.0'), ('127.0.0.2', '127.0.0.2'), ('[::1]', '[::1]')):
            with self.subTest(host=host), tempfile.TemporaryDirectory() as temporary:
                result = subprocess.run(['bash', str(ROOT / 'install.sh'), '--directory', temporary, '--prepare-only', '--admin-email', 'admin@example.test', '--url', f'http://{host}:8080'], text=True, capture_output=True)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn(f'KUBEPHOS_BIND_ADDRESS={expected}\n', Path(temporary, '.env').read_text())

    def test_administrator_is_created_before_app_port_is_published(self):
        tools = Path(self.temp.name) / 'bin'
        tools.mkdir()
        log = Path(self.temp.name) / 'docker.log'
        fake = tools / 'docker'
        fake.write_text('#!/usr/bin/env bash\nset -eu\nprintf "%s\\n" "$*" >> "$DOCKER_TEST_LOG"\nif [[ "$*" == "compose version --short" ]]; then echo 2.32.0; fi\nif [[ "$*" == *setup-admin ]]; then cat >/dev/null; fi\n')
        fake.chmod(0o700)
        env = {**os.environ, 'PATH': str(tools) + os.pathsep + os.environ['PATH'], 'DOCKER_TEST_LOG': str(log)}
        self.run_installer(env=env)
        calls = log.read_text().splitlines()
        setup = next(i for i, call in enumerate(calls) if call.endswith('app setup-admin'))
        start = next(i for i, call in enumerate(calls) if call.endswith('up -d --wait --wait-timeout 180'))
        self.assertLess(setup, start)
        self.assertTrue(any(call.endswith('postgres keycloak migrate seaweedfs') for call in calls[:setup]))
        self.assertNotIn(json.loads((self.directory / 'credentials.json').read_text())['password'], log.read_text())
        self.assertFalse((self.directory / '.install-lock').exists())


if __name__ == '__main__':
    unittest.main()
