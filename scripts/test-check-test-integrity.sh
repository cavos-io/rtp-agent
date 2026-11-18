#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
python3 - "$root/scripts/check-test-integrity.sh" "$(command -v bash)" <<'PY'
import pathlib, subprocess, sys, tempfile

guard, bash = sys.argv[1:]
with tempfile.TemporaryDirectory(prefix="test-integrity-") as directory:
    root = pathlib.Path(directory)
    def git(*args):
        return subprocess.run(["git", *args], cwd=root, check=True, capture_output=True)
    git("init", "-q")
    probe = "examples/native-receive-probe/probe_test.go"
    production = "interface/worker/worker_test.go"
    sibling = "examples/native-receive-probe-other/probe_test.go"
    module = "examples/native-receive-probe/go.mod"
    for path in (probe, production, sibling, module):
        target = root / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text("module probe\n" if path == module else "package probe\n")
    git("add", ".")
    git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid",
        "-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "commit", "-qm", "fixture")
    def check(expected, *paths):
        result = subprocess.run([bash, guard, "--cached", *paths], cwd=root,
                                capture_output=True, text=True)
        assert (result.returncode == 0) == expected, result.stdout + result.stderr
    git("rm", "--cached", probe)
    check(False, probe)  # Partial module removal must fail.
    git("rm", "--cached", module)
    check(True, probe)   # Full module retirement is permitted; local files remain.
    assert (root / probe).is_file()
    git("rm", "--cached", production)
    check(False, probe, production)  # Cannot hide a production deletion.
    git("rm", "--cached", sibling)
    check(False, sibling)  # Similar directory names are not exempt.
    git("reset", "--hard", "HEAD")
    (root / probe).write_text("package probe\nfunc bad() { t.Skip() }\n")
    git("add", probe)
    check(False, probe)  # Probe test weakening is still rejected.
print("Test integrity regression checks passed.")
PY
