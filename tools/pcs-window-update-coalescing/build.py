#!/usr/bin/env python3
"""Remote-only contract validation and paired build; never runs a network lab or deploys."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import time

ROOT = Path(os.environ["GITHUB_WORKSPACE"]).resolve()
SRC = ROOT / "src"
OUT = ROOT / "dist" / "window-update-coalescing"
INPUT = SRC / "caddy" / "tools" / "pcs-window-update-coalescing"
PINS = {
    "caddy": "4b5ce26a35d803b5ceba9d60982edd0879e2bb0a",
    "caddy-base": "97690a054a3f5fab673057c469eda35035ce0e43",
    "net": "58d5eddc71f87b2c9b4890de33b8e062402befcf",
    "net-base": "ca43f763b381b813d5fb2c1344a332a0441f91ab",
    "forwardproxy": "b1e4e257f5f3ca9774cb10744f86684e532f0d2a",
}
TARGET = "^TestBodyReadCoalescing"
ENV = dict(os.environ, GOTOOLCHAIN="local", GOWORK="off", CGO_ENABLED="0",
           GOOS="linux", GOARCH="amd64", GOAMD64="v1", GOMAXPROCS="2",
           GOMEMLIMIT="1GiB")
RESULT = {"status": "INCOMPLETE", "started_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
          "github_sha": os.environ["GITHUB_SHA"], "run_id": os.environ["GITHUB_RUN_ID"],
          "run_attempt": os.environ["GITHUB_RUN_ATTEMPT"], "steps": [], "sources": {}}


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def git(path, *args):
    return subprocess.check_output(["git", "-C", str(path), *args], text=True).strip()


def command(name, args, cwd, *, race=False, expect=0, timeout=600):
    env = dict(ENV)
    if race:
        env["CGO_ENABLED"] = "1"
    began = time.monotonic()
    with (OUT / (name + ".log")).open("wb") as log:
        proc = subprocess.run(args, cwd=cwd, env=env, stdout=log, stderr=subprocess.STDOUT, timeout=timeout)
    step = {"name": name, "command": args, "exit_code": proc.returncode,
            "wall_seconds": time.monotonic() - began, "log_sha256": sha(OUT / (name + ".log")),
            "race": race}
    RESULT["steps"].append(step)
    if proc.returncode != expect:
        raise RuntimeError(f"{name}: exit {proc.returncode}, expected {expect}; see retained log")


def test_events(name, *, top_level=None, expected_failure=False):
    events = []
    for line in (OUT / (name + ".log")).read_text().splitlines():
        if line.startswith("{"):
            events.append(json.loads(line))
    passed = [e for e in events if e.get("Action") == "pass" and e.get("Test") and "/" not in e["Test"]]
    failed = [e for e in events if e.get("Action") == "fail" and e.get("Test")]
    if expected_failure:
        text = (OUT / (name + ".log")).read_text()
        assert 'actual lower Write sizes = [13 13], want [26]' in text, "not the frozen negative-control failure"
        assert any(e.get("Test") == "TestBodyReadCoalescingLowerWrites/coalesced" for e in failed)
        assert not any('build failed' in e.get('Output', '') for e in events)
    else:
        assert not failed and passed, f"{name}: absent successful tests or test failure"
        assert any(e.get("Action") == "pass" and not e.get("Test") for e in events), "package did not pass"
        if top_level is not None:
            assert len(passed) == top_level, (name, len(passed), top_level)
    RESULT.setdefault("test_results", {})[name] = {
        "top_level_pass": len(passed), "test_failures": [e["Test"] for e in failed],
        "skips": [e.get("Test") for e in events if e.get("Action") == "skip"],
        "expected_negative_control": expected_failure,
    }


def prepare_build(name, caddy, net):
    dest = ROOT / "build" / name
    dest.mkdir(parents=True)
    shutil.copyfile(INPUT / "main.go", dest / "main.go")
    shutil.copyfile(INPUT / "build.go.sum", dest / "go.sum")
    mod = (INPUT / "build.go.mod").read_text()
    mod = mod.replace("../../src/caddy", "../../src/" + caddy).replace("../../src/net", "../../src/" + net)
    (dest / "go.mod").write_text(mod)
    return dest


def prepare_test_module(project):
    # Test-only dependencies belong to each native module, not the smaller CLI
    # build module. Keep originals and the paired binary dependency locks intact.
    dest = ROOT / "test-locks" / project
    dest.mkdir(parents=True)
    mod = (SRC / project / "go.mod").read_text()
    for name, local in (("github.com/caddyserver/caddy/v2", "caddy"),
                        ("github.com/caddyserver/forwardproxy", "forwardproxy"),
                        ("golang.org/x/net", "net")):
        assert "replace " + name not in mod
        mod += "\nreplace " + name + " => " + str(SRC / local) + "\n"
    (dest / "go.mod").write_text(mod)
    shutil.copyfile(SRC / project / "go.sum", dest / "go.sum")
    return dest


def main():
    OUT.mkdir(parents=True, exist_ok=False)
    assert os.environ.get("GITHUB_ACTIONS") == "true", "remote GitHub Actions only"
    assert os.environ["GITHUB_REPOSITORY"] == "justinchen13/caddy"
    assert os.environ["GITHUB_REF"] == "refs/heads/pcs/window-update-coalescing-20261018"
    command("go-version", ["go", "version"], ROOT)
    assert (OUT / "go-version.log").read_text().strip() == "go version go1.25.6 linux/amd64"
    for project, pin in PINS.items():
        path = SRC / project
        expanded = git(path, "rev-parse", pin + "^{commit}")
        head = git(path, "rev-parse", "HEAD")
        if project == "caddy":
            assert head == os.environ["GITHUB_SHA"]
            changed = git(path, "diff", "--name-only", expanded, head).splitlines()
            assert all(p.startswith(("tools/pcs-window-update-coalescing/", ".github/workflows/pcs-window-update-coalescing.yml")) for p in changed), changed
        else:
            assert head == expanded
        assert not git(path, "status", "--porcelain", "--untracked-files=no")
        RESULT["sources"][project] = {"runtime_commit": expanded, "checkout_commit": head}
    assert sha(SRC / "net/http2/server.go") == "b27ec8c6e7ffbfd9fd65971968bb3928917aac86a19fa4b9ebccd59dd4b14085"
    assert sha(SRC / "net/http2/server_window_update_coalescing_test.go") == "c928d1e377d428329fd1952aafad84f699f4ed7da3860dd541cca430505a5724"
    tracked_locks = {str(p.relative_to(ROOT)): sha(p) for project in PINS for p in (SRC / project / "go.mod", SRC / project / "go.sum")}
    # Overlay the identical test only; the baseline production runtime is intact.
    shutil.copyfile(SRC / "net/http2/server_window_update_coalescing_test.go", SRC / "net-base/http2/server_window_update_coalescing_test.go")
    command("old-negative", ["go", "test", "-mod=readonly", "-json", "-count=1", "-timeout=90s", "-run", "^TestBodyReadCoalescingLowerWrites/coalesced$", "./http2"], SRC / "net-base", expect=1)
    test_events("old-negative", expected_failure=True)
    for race in (False, True):
        name = "candidate-target-race" if race else "candidate-target"
        args = ["go", "test", "-mod=readonly", "-json", "-count=1", "-timeout=120s", "-run", TARGET]
        if race:
            args.append("-race")
        command(name, args + ["./http2"], SRC / "net", race=race)
        test_events(name, top_level=8)
    command("net-regression", ["go", "test", "-mod=readonly", "-json", "-count=1", "-timeout=300s", "./http2", "./http2/hpack"], SRC / "net")
    test_events("net-regression")
    builds = {"baseline": prepare_build("baseline", "caddy-base", "net-base"),
              "candidate": prepare_build("candidate", "caddy", "net")}
    build_locks = {str(p.relative_to(ROOT)): sha(p) for d in builds.values() for p in (d / "go.mod", d / "go.sum")}
    for name, cwd in builds.items():
        command(name + "-dependencies", ["go", "list", "-mod=readonly", "-deps", "-json", "-tags=nobadger,nomysql,nopgx", "."], cwd)
        command(name + "-module-resolution", ["go", "list", "-m", "-json", "github.com/caddyserver/caddy/v2", "github.com/caddyserver/forwardproxy", "golang.org/x/net"], cwd)
        binary = OUT / ("caddy-" + name)
        version = "v2.11.1-pcsproxy8-wu-baseline" if name == "baseline" else "v2.11.1-pcsproxy9-wu-candidate"
        command(name + "-build", ["go", "build", "-p", "2", "-mod=readonly", "-buildvcs=false", "-trimpath", "-tags=nobadger,nomysql,nopgx", "-ldflags", "-s -w -X github.com/caddyserver/caddy/v2.CustomVersion=" + version, "-o", str(binary), "."], cwd)
        command(name + "-buildinfo", ["go", "version", "-m", str(binary)], cwd)
        command(name + "-version", [str(binary), "version"], cwd)
        command(name + "-modules", [str(binary), "list-modules"], cwd)
        modules = (OUT / (name + "-modules.log")).read_text()
        assert "http.handlers.forward_proxy" in modules
        RESULT.setdefault("binaries", {})[name] = {"sha256": sha(binary), "bytes": binary.stat().st_size, "custom_version": version}
    test_modules = {project: prepare_test_module(project) for project in ("caddy", "forwardproxy")}
    test_locks = {str(p.relative_to(ROOT)): sha(p) for d in test_modules.values() for p in (d / "go.mod", d / "go.sum")}
    for race in (False, True):
        name = "caddy-proxy-regression-race" if race else "caddy-proxy-regression"
        args = ["go", "test", "-modfile=" + str(test_modules["caddy"] / "go.mod"), "-mod=readonly", "-json", "-count=1", "-timeout=180s", "-tags=nobadger,nomysql,nopgx", "-run", "^TestProxy"]
        if race:
            args.append("-race")
        command(name, args + ["./modules/caddyhttp"], SRC / "caddy", race=race)
        test_events(name)
    command("forwardproxy-regression", ["go", "test", "-modfile=" + str(test_modules["forwardproxy"] / "go.mod"), "-mod=readonly", "-json", "-count=1", "-timeout=180s", "-tags=nobadger,nomysql,nopgx", "."], SRC / "forwardproxy")
    test_events("forwardproxy-regression")
    for rel, expected in {**tracked_locks, **build_locks, **test_locks}.items():
        assert sha(ROOT / rel) == expected, "module lock changed: " + rel
    RESULT["module_locks_unchanged"] = True
    RESULT["status"] = "CONTRACTS_AND_PAIRED_BUILD_PASS_NOT_PERFORMANCE_ACCEPTANCE"


try:
    main()
except BaseException as exc:
    RESULT["status"] = "FAIL"
    RESULT["error"] = str(exc)
    raise
finally:
    OUT.mkdir(parents=True, exist_ok=True)
    RESULT["ended_utc"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    RESULT["build_script_sha256"] = sha(__file__)
    (OUT / "result.json").write_text(json.dumps(RESULT, indent=2) + "\n")
