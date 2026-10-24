"""Package a version-pinned Linux installer without development files or secrets."""
import hashlib
from pathlib import Path
import re
import sys
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def release_permissions(entry):
    entry.mode = 0o755 if entry.name.endswith('.sh') else 0o644
    return entry


def package(tag, destination):
    if not re.fullmatch(r'v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?', tag):
        raise ValueError('release tag must be vMAJOR.MINOR.PATCH with an optional prerelease suffix')
    destination = Path(destination)
    destination.mkdir(parents=True, exist_ok=True)
    name = f'kubephos-{tag}'
    archive = destination / f'{name}.tar.gz'
    files = ['update.sh', 'compose.yaml', 'README.md', 'LICENSE', 'THIRD_PARTY_NOTICES.md', 'SECURITY.md', 'CONTRIBUTING.md', 'components/mentat/LICENSE']
    files += [str(path.relative_to(ROOT)) for path in sorted((ROOT / 'docs').glob('*.md'))]
    with tempfile.TemporaryDirectory() as temporary:
        installer = Path(temporary) / 'install.sh'
        installer.write_text((ROOT / 'install.sh').read_text().replace('release_version=latest\n', f'release_version={tag}\n', 1))
        installer.chmod(0o755)
        with tarfile.open(archive, 'w:gz') as bundle:
            for file in files:
                bundle.add(ROOT / file, arcname=f'{name}/{file}', recursive=False, filter=release_permissions)
            bundle.add(installer, arcname=f'{name}/install.sh')
    digest = hashlib.sha256(archive.read_bytes()).hexdigest()
    (destination / 'SHA256SUMS').write_text(f'{digest}  {archive.name}\n')
    return archive


if __name__ == '__main__':
    if len(sys.argv) != 3:
        raise SystemExit('usage: python3 scripts/package_release.py <tag> <output-directory>')
    print(package(sys.argv[1], sys.argv[2]))
