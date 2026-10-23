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
OUT = ROOT / "dist" / "send-credit-stall"
INPUT = SRC / "caddy" / "tools" / "pcs-send-credit-stall"
PINS = {
    "caddy": "0c4c4a8dcb757eb3d9319ab3e73d169510ebb5ae",
    "caddy-base": "97690a054a3f5fab673057c469eda35035ce0e43",
    "net": "4ccc1ff593088a49c323ec458840e5ae13383a85",
    "net-base": "ca43f763b381b813d5fb2c1344a332a0441f91ab",
    "forwardproxy": "b1e4e257f5f3ca9774cb10744f86684e532f0d2a",
}
TARGET = "^TestProxy(SendCredit|GoAway)"
REQUIRED_TARGETS = set(['TestProxyGoAwayFlushErrorPropagated', 'TestProxyGoAwayFrameErrorPreserved', 'TestProxyGoAwaySuccessfulWireFrame', 'TestProxySendCreditConservationSplitAndCancel', 'TestProxySendCreditControlAndStreamUpdates', 'TestProxySendCreditControlBusyAcrossTimeout', 'TestProxySendCreditDATACompletionWithDoneResets', 'TestProxySendCreditDrainConcurrentInitialization', 'TestProxySendCreditDrainConcurrentReservations', 'TestProxySendCreditDrainGoAwayWriteErrorReleases', 'TestProxySendCreditDrainInitializationAndCopies', 'TestProxySendCreditDrainNativeLeaseLifetime', 'TestProxySendCreditDrainObservationGatesAndTimeout', 'TestProxySendCreditFreshACKBeforeCompletionDrains', 'TestProxySendCreditProbeACKAndFlushOrder', 'TestProxySendCreditProbeActiveFrameAndDATAReset', 'TestProxySendCreditProbeLateACKAndError', 'TestProxySendCreditStallEligibility', 'TestProxySendCreditStallNativeDrain', 'TestProxySendCreditStallNoACKDoesNotClose', 'TestProxySendCreditStallWUInvalidatesProbe', 'TestProxySendCreditTransportRepoolKeepsAcceptedStream'])
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


def test_events(name):
    events = [json.loads(line) for line in (OUT / (name + ".log")).read_text().splitlines() if line.startswith("{")]
    passed = [e for e in events if e.get("Action") == "pass" and e.get("Test") and "/" not in e["Test"]]
    failed = [e for e in events if e.get("Action") == "fail"]
    assert passed and not failed, f"{name}: missing successful tests or failures"
    packages = {e["Package"] for e in events if e.get("Action") == "pass" and not e.get("Test")}
    expected = {"golang.org/x/net/http2", "golang.org/x/net/http2/hpack"} if name == "net-regression" else None
    assert packages and (expected is None or packages == expected), (name, packages, expected)
    names = {e["Test"] for e in passed}
    if name in ("candidate-target", "candidate-target-race"):
        assert REQUIRED_TARGETS <= names, (name, sorted(REQUIRED_TARGETS - names))
    if name.startswith("caddy-proxy-regression"):
        assert "TestProxySendCreditStallOptInAndAuthenticatedContext" in names
    RESULT.setdefault("test_results", {})[name] = {"top_level_pass": len(passed),
        "passed_tests": sorted(names), "skips": [e.get("Test") for e in events if e.get("Action") == "skip"],
        "packages_passed": sorted(packages)}


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
    assert os.environ["GITHUB_REF"] == "refs/heads/pcs/send-credit-stall-20261023"
    command("go-version", ["go", "version"], ROOT)
    assert (OUT / "go-version.log").read_text().strip() == "go version go1.25.6 linux/amd64"
    for project, pin in PINS.items():
        path = SRC / project
        expanded = git(path, "rev-parse", pin + "^{commit}")
        head = git(path, "rev-parse", "HEAD")
        if project == "caddy":
            assert head == os.environ["GITHUB_SHA"]
            changed = git(path, "diff", "--name-only", expanded, head).splitlines()
            assert all(p.startswith(("tools/pcs-send-credit-stall/", ".github/workflows/pcs-send-credit-stall.yml")) for p in changed), changed
        else:
            assert head == expanded
        assert not git(path, "status", "--porcelain", "--untracked-files=no")
        RESULT["sources"][project] = {"runtime_commit": expanded, "checkout_commit": head}
    tracked_locks = {str(p.relative_to(ROOT)): sha(p) for project in PINS for p in (SRC / project / "go.mod", SRC / project / "go.sum")}
    for race in (False, True):
        name = "candidate-target-race" if race else "candidate-target"
        args = ["go", "test", "-mod=readonly", "-json", "-count=1", "-timeout=120s", "-run", TARGET]
        if race:
            args.append("-race")
        command(name, args + ["./http2"], SRC / "net", race=race)
        test_events(name)
    command("net-regression", ["go", "test", "-mod=readonly", "-json", "-count=1", "-timeout=300s", "./http2", "./http2/hpack"], SRC / "net")
    test_events("net-regression")
    builds = {"baseline": prepare_build("baseline", "caddy-base", "net-base"),
              "candidate": prepare_build("candidate", "caddy", "net")}
    build_locks = {str(p.relative_to(ROOT)): sha(p) for d in builds.values() for p in (d / "go.mod", d / "go.sum")}
    for name, cwd in builds.items():
        command(name + "-dependencies", ["go", "list", "-mod=readonly", "-deps", "-json", "-tags=nobadger,nomysql,nopgx", "."], cwd)
        command(name + "-module-resolution", ["go", "list", "-m", "-json", "github.com/caddyserver/caddy/v2", "github.com/caddyserver/forwardproxy", "golang.org/x/net"], cwd)
        binary = OUT / ("caddy-" + name)
        version = "v2.11.1-pcsproxy8-credit-baseline" if name == "baseline" else "v2.11.1-pcsproxy9-credit-candidate"
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
