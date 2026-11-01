"""Migration files remain readable by Liquibase after restrictive archive extraction."""
import json
import re
import shlex
import stat
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


class MigrationPermissionsTests(unittest.TestCase):
    def test_copy_repairs_existing_permissions_with_restrictive_umask(self):
        compose = (ROOT / 'compose.yaml').read_text()
        service = compose.split('  migration-files:\n', 1)[1].split('\n  migrate:', 1)[0]
        command = json.loads(re.search(r'^    command: (.+)$', service, re.MULTILINE).group(1))[0]
        with tempfile.TemporaryDirectory(prefix='kubephos migration permissions ') as directory:
            source, target = Path(directory) / 'source', Path(directory) / 'target'
            for path in (source, target):
                (path / 'changes').mkdir(parents=True, mode=0o700)
                (path / 'db.changelog.yaml').write_text('databaseChangeLog: []\n')
                (path / 'changes' / 'example.sql').write_text('SELECT 1;\n')
                for file in path.rglob('*'):
                    file.chmod(0o700 if file.is_dir() else 0o600)
                path.chmod(0o700)
            command = command.replace('/opt/kubephos/migrations/.', shlex.quote(str(source) + '/.'))
            command = command.replace('/liquibase/changelog', shlex.quote(str(target)))
            subprocess.run(['sh', '-ec', 'umask 077; ' + command], check=True, capture_output=True)
            for path in [target, *target.rglob('*')]:
                self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o755 if path.is_dir() else 0o644, path)
            self.assertEqual((target / 'changes' / 'example.sql').read_text(), 'SELECT 1;\n')
