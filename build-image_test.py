#!/usr/bin/env python3
"""Exercise the shared image builder in a disposable filesystem, without Docker."""
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile

source = Path(__file__).resolve().parent
assert os.geteuid() == 0, "Run this chroot check as root"

for bundled in (True, False):
    with tempfile.TemporaryDirectory(prefix="the8020-image-test-") as directory:
        root = Path(directory)

        def copy(path):
            path = Path(path)
            target = root / path.relative_to("/")
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(path, target)

        for command in ("bash", "install", "mkdir", "rm", "mv", "ln", "grep",
                        "cat", "tar", "sha256sum", "cut", "find"):
            binary = shutil.which(command)
            assert binary, command
            copy(binary)
            libraries = subprocess.check_output(["ldd", binary], text=True)
            for library in set(re.findall(r"(/[^\s()]+)", libraries)):
                copy(library)
        (root / "bin").mkdir(exist_ok=True)
        if not (root / "bin/bash").exists():
            copy("/bin/bash")
        shutil.copy2(source / "build-image.sh", root / "build-image.sh")

        def write(path, contents):
            target = root / path.lstrip("/")
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text(contents)
            target.chmod(0o755)

        write("/bin/git", '''#!/bin/bash
set -eu
if [[ "$1" == clone ]]; then exit; fi
case "$3" in
  tag)
    [[ "$*" == *'--list 0.7.* --sort=-version:refname' ]]
    [[ ! -f /no-release ]] || exit 0
    printf '%s\n' 0.7.99-rc1 0.7.10 0.7.9 0.7.0
    ;;
  checkout) printf '%s\n' "${!#}" > /selected-tag ;;
  rev-parse) printf '%s\n' fixture-commit ;;
  *) exit 1 ;;
esac
''')
        write("/usr/local/src/the8020/run.sh", '''#!/bin/bash
set -eu
IFS= read -r command
[[ "$command" == exit && "$PWD" == /8020 && "$THE8020_RELEASE_VERSION" == 0.7 ]]
mkdir -p node/kernel/bin node/kernel/runtime/{definitions,images/rootless,images/workspace,downloads,gvisor,tmp,verification-deno-cache,observed}
printf engine > node/kernel/bin/engine
printf sdk > node/kernel/runtime/definitions/sdk
printf '{}' > node/kernel/runtime/images/rootless/image.json
printf '{}' > node/kernel/runtime/images/workspace/image.json
''')
        write("/usr/local/src/the8020/docker/rootfs/usr/local/bin/docker-entrypoint.sh",
              "readonly RUNTIME_STATE=/usr/local/share/the8020/runtime-state\n"
              if bundled else "#!/bin/bash\n")

        def run(version):
            return subprocess.run(
                [shutil.which("chroot"), str(root), "/bin/bash", "/build-image.sh", version],
                env={**os.environ, "PATH": "/bin:/usr/bin", "LC_ALL": "C"},
                text=True, capture_output=True, timeout=15,
            )

        for version in ("", "0", "0.7.1", "00.7", "0.07", "0.7-rc1"):
            result = run(version)
            assert result.returncode != 0 and "VERSION must" in result.stderr
        (root / "no-release").touch()
        result = run("0.7")
        assert result.returncode != 0 and "no kernel tag" in result.stderr, result.stderr
        (root / "no-release").unlink()
        result = run("0.7")
        assert result.returncode == 0, result.stderr
        assert (root / "selected-tag").read_text().strip() == "0.7.10"
        payload = root / "usr/local/share/the8020"
        assert (payload / "release").read_text() == (
            "release_line=0.7\nkernel_tag=0.7.10\nkernel_commit=fixture-commit\n")
        assert (payload / "runtime-bin/engine").read_text() == "engine"
        assert (root / "8020/node/kernel/bin").readlink() == Path(
            "/usr/local/share/the8020/runtime-bin")
        runtime = root / "8020/node/kernel/runtime"
        assert {path.name for path in runtime.iterdir()} == (
            set() if bundled else {"definitions", "images"})
        if bundled:
            assert re.fullmatch(r"[0-9a-f]{64}\n", (payload / "runtime-state/id").read_text())
            assert (payload / "runtime-state/definitions/sdk").read_text() == "sdk"
        else:
            assert not (payload / "runtime-state").exists()

print("Shared image build checks passed")
