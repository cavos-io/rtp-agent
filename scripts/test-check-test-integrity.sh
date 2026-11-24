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

# Consolidation must retain bodies, helpers, imports and build constraints.
python3 - "$root/scripts/check-test-integrity.sh" "$(command -v bash)" <<'PY'
import pathlib, subprocess, sys, tempfile

guard, bash = sys.argv[1:]
with tempfile.TemporaryDirectory(prefix="test-consolidation-") as directory:
    root = pathlib.Path(directory)
    def git(*args):
        return subprocess.run(["git", *args], cwd=root, check=True, capture_output=True)
    git("init", "-q")
    header = '//go:build integration\n\npackage sample\nimport "testing"\n'
    first = 'func TestFirst(t *testing.T) { if helper() != 1 { t.Fatal("wrong") } }\n'
    helper = 'func helper() int { return 1 }\n'
    second = 'func TestSecond(t *testing.T) { t.Log("second") }\n'
    (root / "first_test.go").write_text(header + first + helper)
    (root / "second_test.go").write_text(header + second)
    git("add", ".")
    git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid",
        "-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "commit", "-qm", "fixture")
    def check(expected, contents, name="second_test.go", cached=True):
        git("reset", "--hard", "HEAD")
        git("clean", "-fd")
        (root / "first_test.go").unlink()
        if name != "second_test.go":
            (root / "second_test.go").unlink()
        (root / name).parent.mkdir(parents=True, exist_ok=True)
        (root / name).write_text(contents)
        if cached: git("add", "-A")
        result = subprocess.run([bash, guard, "--cached" if cached else "HEAD",
                                 "first_test.go", "second_test.go", name],
                                cwd=root, capture_output=True, text=True)
        assert (result.returncode == 0) == expected, result.stdout + result.stderr
    merged = header + second + first + helper
    check(True, merged)
    check(True, merged, cached=False)
    check(True, merged.replace('import "testing"', 'import tt "testing"').replace('*testing.T', '*tt.T'))
    check(False, merged, name="_hidden_test.go")
    check(False, merged, name="testdata/merged_test.go")
    check(False, header + second + helper)  # Missing test.
    check(False, merged.replace('t.Fatal("wrong")', 't.Log("wrong")'))
    check(False, merged.replace('return 1', 'return 2'))  # Changed helper.
    check(False, merged.replace('integration', 'integration && native'))
    check(False, merged, name="second_linux_test.go")  # Implicit platform constraint.
    check(False, merged.replace('"testing"', '"other/testing"'))
    check(False, merged + 'func TestSkip(t *testing.T) { t.Skip() }\n')
    git("reset", "--hard", "HEAD")
    git("clean", "-fd")
    (root / "nested").mkdir()
    (root / "nested/go.mod").write_text("module separate\n")
    (root / "nested/merged_test.go").write_text(merged)
    git("rm", "first_test.go", "second_test.go")
    git("add", ".")
    result = subprocess.run([bash, guard, "--cached", "first_test.go", "second_test.go",
                             "nested/merged_test.go"], cwd=root, capture_output=True, text=True)
    assert result.returncode != 0, result.stdout + result.stderr
    # A pre-existing copy, even with unrelated additions, is not new coverage.
    git("reset", "--hard", "HEAD")
    git("clean", "-fd")
    (root / "copy").mkdir()
    (root / "copy/existing_test.go").write_text(header + first + helper)
    git("add", ".")
    git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid",
        "-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "commit", "-qm", "existing copy")
    git("rm", "first_test.go")
    (root / "copy/existing_test.go").write_text(header + first + helper + second)
    git("add", ".")
    result = subprocess.run([bash, guard, "--cached", "first_test.go", "copy/existing_test.go"],
                            cwd=root, capture_output=True, text=True)
    assert result.returncode != 0, result.stdout + result.stderr
    # Unstaged restoration cannot excuse a staged deletion.
    git("reset", "--hard", "HEAD")
    git("rm", "first_test.go")
    (root / "second_test.go").write_text(merged)
    result = subprocess.run([bash, guard, "--cached", "first_test.go", "second_test.go"],
                            cwd=root, capture_output=True, text=True)
    assert result.returncode != 0, result.stdout + result.stderr
print("Consolidation regression checks passed.")
PY

# Upstream-only additions are not deletions by a diverged feature branch.
python3 - "$root/scripts/check-test-integrity.sh" "$(command -v bash)" <<'PY'
import pathlib, subprocess, sys, tempfile

guard, bash = sys.argv[1:]
with tempfile.TemporaryDirectory(prefix="test-integrity-base-") as directory:
    root = pathlib.Path(directory)
    def git(*args):
        return subprocess.run(["git", *args], cwd=root, check=True, capture_output=True)
    def commit():
        git("add", "-A")
        git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid",
            "-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "commit", "-qm", "fixture")
    git("init", "-q")
    path = root / "coverage_test.go"
    original = 'package sample\nimport "testing"\nfunc TestOriginal(t *testing.T) {\n t.Fatal("original")\n}\n'
    path.write_text(original)
    commit()
    git("branch", "feature")
    git("checkout", "-qb", "upstream")
    path.write_text(original + 'func TestUpstream(t *testing.T) { t.Fatal("upstream") }\n')
    commit()
    git("checkout", "feature")
    def check(expected, base="upstream"):
        result = subprocess.run([bash, guard, base, "coverage_test.go"],
                                cwd=root, capture_output=True, text=True)
        assert (result.returncode == 0) == expected, result.stdout + result.stderr
    check(True)
    path.write_text(original.replace(' t.Fatal("original")\n', ''))
    check(False)  # Real assertion removal remains rejected.
    git("checkout", "--", "coverage_test.go")
    path.unlink()
    check(False)  # Real test deletion remains rejected.
    git("add", "-A")
    check(False, "--cached")
    git("reset", "--hard", "HEAD")
    git("merge", "--ff-only", "upstream")
    path.write_text(original)
    check(False)  # Once inherited, upstream tests cannot be removed either.
    check(False, "missing-reference")  # Missing base must fail closed.
print("Diverged-base regression checks passed.")
PY
