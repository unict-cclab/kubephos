import hashlib
from pathlib import Path
import tarfile
import tempfile
import unittest

from package_release import package


class ReleaseTests(unittest.TestCase):
    def test_archive_pins_version_and_excludes_local_configuration(self):
        with tempfile.TemporaryDirectory() as directory:
            archive = package('v0.9.0-rc.1', directory)
            with tarfile.open(archive) as bundle:
                files = bundle.getnames()
                self.assertFalse(any(Path(file).name in ('.env', 'credentials.json', 'compose.dev.yaml', 'dev.env') for file in files))
                self.assertIn('kubephos-v0.9.0-rc.1/LICENSE', files)
                installer = bundle.extractfile('kubephos-v0.9.0-rc.1/install.sh').read().decode()
                self.assertIn('release_version=v0.9.0-rc.1\n', installer)
                self.assertEqual(bundle.getmember('kubephos-v0.9.0-rc.1/install.sh').mode, 0o755)
            self.assertEqual(Path(directory, 'SHA256SUMS').read_text(), f'{hashlib.sha256(archive.read_bytes()).hexdigest()}  {archive.name}\n')

    def test_invalid_tag_is_rejected_before_writing(self):
        with tempfile.TemporaryDirectory() as directory:
            for tag in ('latest', 'v1', 'v1.2.3/../other', 'v$(false)'):
                with self.assertRaises(ValueError):
                    package(tag, directory)
            self.assertEqual(list(Path(directory).iterdir()), [])


if __name__ == '__main__':
    unittest.main()
