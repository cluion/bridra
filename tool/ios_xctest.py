"""Run the existing HTTP integration flow through Flutter's native XCTest bridge."""

import argparse
from concurrent.futures import ThreadPoolExecutor
import hashlib
import json
import os
from pathlib import Path
import signal
import subprocess
import time


FAILURE_MARKER = "Bridra intentional Dart assertion failure"
RECEIPTS = (
    '"rpc_method":"system.health"', '"rpc_method":"greeting.hello"',
    '"rpc_method":"bridra.smoke.stream"', '"rpc_method":"bridra.smoke.download"',
    '"surface":"file_transfer"', '"rpc_method":"bridra.smoke.upload.verify"',
    'server: smoke download interrupted at offset 32768',
    'server: smoke download resumed at offset 32768',
    'server: smoke upload interrupted at offset 32768',
    'server: smoke upload resumed at offset 32768',
)


def validate_results(summary, status, expected_failure):
    counts = ("totalTestCount", "passedTests", "failedTests", "skippedTests", "expectedFailures")
    if any(type(summary.get(key)) is not int for key in counts):
        raise ValueError("missing or invalid XCTest result counts")
    if (summary.get("totalTestCount") != 1 or summary.get("skippedTests") != 0
            or summary.get("expectedFailures") != 0):
        raise ValueError("XCTest must execute exactly one complete, unskipped native test")
    if expected_failure:
        failures = summary.get("testFailures")
        if (status != 65 or summary.get("failedTests") != 1 or summary.get("passedTests") != 0
                or not isinstance(failures, list) or len(failures) != 1
                or FAILURE_MARKER not in failures[0].get("failureText", "")):
            raise ValueError("expected the intentional Dart assertion to fail XCTest")
    elif status != 0 or summary.get("failedTests") != 0 or summary.get("passedTests") != 1:
        raise ValueError("required XCTest did not pass")


def validate_receipts(text):
    missing = [receipt for receipt in RECEIPTS if receipt not in text]
    if missing:
        raise ValueError("missing backend integration receipts: " + ", ".join(missing))


def source_identity(root):
    paths = subprocess.check_output([
        "git", "ls-files", "--cached", "--others", "--exclude-standard", "-z", "--",
        ".fvmrc", "pubspec.yaml", "pubspec.lock", "lib", "integration_test", "ios",
        "packages/bridra_flutter/lib", "packages/bridra_flutter/pubspec.yaml",
    ], cwd=root).split(b"\0")
    digest = hashlib.sha256()
    for name in sorted(set(paths) - {b""}):
        path = root / os.fsdecode(name)
        if path.is_file():
            digest.update(name + b"\0" + path.read_bytes() + b"\0")
    return digest.hexdigest()


def stop(process):
    if process.poll() is not None:
        return
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except ProcessLookupError:
        return
    try:
        process.wait(timeout=5)
    except subprocess.TimeoutExpired:
        os.killpg(process.pid, signal.SIGKILL)
        process.wait(timeout=5)


class Experiment:
    def __init__(self, root, output):
        self.root, self.output = root, output
        self.stages = []
        self.processes = []
        output.mkdir(parents=True, exist_ok=True)

    def run(self, name, command, timeout=540, allowed=(0,)):
        started = time.monotonic()
        print(f"Starting {name}", flush=True)
        with (self.output / (name + ".log")).open("wb") as log:
            process = subprocess.Popen(command, cwd=self.root, stdout=log,
                                       stderr=subprocess.STDOUT, start_new_session=True)
            self.processes.append(process)
            try:
                status = process.wait(timeout=timeout)
            except subprocess.TimeoutExpired:
                stop(process)
                raise TimeoutError(f"{name} exceeded {timeout}s")
            except BaseException:
                for owned in self.processes:
                    stop(owned)
                raise
        elapsed = round(time.monotonic() - started, 3)
        self.stages.append({"name": name, "seconds": elapsed, "exitCode": status})
        print(f"Finished {name}: {elapsed}s, exit {status}", flush=True)
        if status not in allowed:
            raise RuntimeError(f"{name} failed: see {self.output / (name + '.log')}")
        return status


def execute(args):
    root, output = Path(args.root).resolve(), Path(args.output).resolve()
    experiment = Experiment(root, output)
    device = args.device
    devices = json.loads(subprocess.check_output(["xcrun", "simctl", "list", "devices", "available", "--json"]))
    available = [d for group in devices["devices"].values() for d in group if d["name"].startswith("iPhone")]
    if device:
        selected = next(d for d in available if d["udid"] == device)
    else:
        selected = next((d for d in available if d["state"] == "Booted"), available[0])
        device = selected["udid"]
    booted_here = selected["state"] != "Booted"
    build = Path(args.build_directory).resolve()
    build.mkdir(parents=True, exist_ok=True)
    xcode_version = subprocess.check_output(["xcodebuild", "-version"], text=True)
    identity = {"source": source_identity(root), "xcode": xcode_version,
                "port": args.port, "expectedFailure": args.expect_failure}
    manifest = build / "identity.json"
    if args.reuse_build and (not manifest.exists() or json.loads(manifest.read_text()) != identity):
        raise ValueError("reused XCTest artifacts must match the current source, toolchain and defines")
    server = None
    total_started = time.monotonic()
    success = False
    try:
        with ThreadPoolExecutor(max_workers=1) as pool:
            if booted_here:
                boot = pool.submit(experiment.run, "simulator-boot", ["/bin/sh", "-c",
                                   'xcrun simctl boot "$1" && xcrun simctl bootstatus "$1" -b',
                                   "bridra-simulator", device], 240)
            else:
                boot = None
            experiment.run("backend-build", ["make", "backend-server-build"], 240)
            if not args.reuse_build:
                defines = {"BRIDRA_BACKEND_URL": f"http://127.0.0.1:{args.port}/rpc",
                           "BRIDRA_BACKEND_TOKEN": "ios-simulator-smoke-token",
                           "BRIDRA_SMOKE_STREAM": "true", "BRIDRA_SMOKE_DOWNLOAD": "true",
                           "BRIDRA_SMOKE_UPLOAD_RESUME": "true", "BRIDRA_SMOKE_CLIENT": "iOS Simulator"}
                target = ("integration_test/ios_expected_failure.dart" if args.expect_failure
                          else "integration_test/ios_xctest_smoke.dart")
                experiment.run("flutter-configure", ["fvm", "flutter", "build", "ios", "--config-only",
                               "--simulator", "--debug", f"--target={target}",
                               *[f"--dart-define={key}={value}" for key, value in defines.items()]], 240)
                experiment.run("xctest-build", ["xcodebuild", "build-for-testing", "-workspace",
                               "ios/Runner.xcworkspace", "-scheme", "Runner", "-configuration", "Debug",
                               "-sdk", "iphonesimulator", "-destination", f"platform=iOS Simulator,id={device}",
                               "-derivedDataPath", str(build / "DerivedData"), "-parallel-testing-enabled", "NO",
                               "CODE_SIGNING_ALLOWED=NO", "-showBuildTimingSummary"], 540)
                # Configuration generation may update tracked Xcode project metadata.
                identity["source"] = source_identity(root)
                manifest.write_text(json.dumps(identity, indent=2) + "\n")
            if boot:
                boot.result()
        server_log = output / "backend.log"
        with server_log.open("wb") as log:
            server = subprocess.Popen([str(root / "build/server/bridra_server"),
                                       "--listen", f"127.0.0.1:{args.port}", "--token", "ios-simulator-smoke-token",
                                       "--smoke-stream", "--smoke-download", "--smoke-download-resume",
                                       "--smoke-upload-resume", "--cors-origin", "*"],
                                      stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
            deadline = time.monotonic() + 10
            while "server: listening on " not in server_log.read_text():
                if server.poll() is not None or time.monotonic() >= deadline:
                    raise RuntimeError("Go HTTP backend failed to start")
                time.sleep(0.1)
            artifacts = list((build / "DerivedData/Build/Products").glob("*.xctestrun"))
            if len(artifacts) != 1:
                raise ValueError("expected exactly one compiled XCTest configuration")
            result = output / "result.xcresult"
            status = experiment.run("xctest-run", ["xcodebuild", "test-without-building", "-xctestrun",
                                    str(artifacts[0]), "-destination", f"platform=iOS Simulator,id={device}",
                                    "-parallel-testing-enabled", "NO", "-resultBundlePath", str(result),
                                    "-test-timeouts-enabled", "YES", "-maximum-test-execution-time-allowance", "180"],
                                    300, (0, 65))
            experiment.run("result-summary", ["xcrun", "xcresulttool", "get", "test-results", "summary",
                                              "--path", str(result), "--compact"], 30)
            summary = json.loads((output / "result-summary.log").read_text())
            validate_results(summary, status, args.expect_failure)
            if not args.expect_failure:
                validate_receipts(server_log.read_text())
            success = True
    finally:
        for process in experiment.processes + ([server] if server else []):
            stop(process)
        if booted_here:
            experiment.run("simulator-shutdown", ["xcrun", "simctl", "shutdown", device], 30)
        report = {"success": success, "expectedFailure": args.expect_failure, "reusedBuild": args.reuse_build,
                  "source": identity["source"], "device": device, "xcode": xcode_version,
                  "wallSeconds": round(time.monotonic() - total_started, 3), "stages": experiment.stages}
        (output / "timing.json").write_text(json.dumps(report, indent=2) + "\n")


def install_signal_handlers():
    def interrupted(signum, frame):
        raise KeyboardInterrupt("iOS experiment interrupted")
    signal.signal(signal.SIGTERM, interrupted)


def main():
    install_signal_handlers()
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", default=".")
    parser.add_argument("--device", default=os.environ.get("BRIDRA_IOS_SIMULATOR_DEVICE", ""))
    parser.add_argument("--output", required=True)
    parser.add_argument("--build-directory", required=True)
    parser.add_argument("--port", type=int, default=18080)
    parser.add_argument("--reuse-build", action="store_true")
    parser.add_argument("--expect-failure", action="store_true")
    execute(parser.parse_args())


if __name__ == "__main__":
    main()
