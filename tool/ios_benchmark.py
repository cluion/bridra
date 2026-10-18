"""Compare unchanged HTTP smoke assertions on Flutter and native XCTest runners."""

import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import time

from ios_xctest import Experiment, install_signal_handlers


def main():
    install_signal_handlers()
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--runner", choices=("flutter", "xctest"), required=True)
    parser.add_argument("--phase", choices=("cold", "warm"), required=True)
    parser.add_argument("--reset-replay", action="store_true")
    args = parser.parse_args()
    if args.reset_replay and os.environ.get("GITHUB_ACTIONS") != "true":
        raise ValueError("reset replay is restricted to disposable hosted experiment runners")
    root = Path.cwd()
    output = root / "build/ios-experiment" / args.phase / args.runner
    output.mkdir(parents=True, exist_ok=True)
    experiment = Experiment(root, output)
    results = []
    success = False
    devices = json.loads(subprocess.check_output(["xcrun", "simctl", "list", "devices", "available", "--json"]))
    device = next(d["udid"] for group in devices["devices"].values() for d in group
                  if d["name"].startswith("iPhone"))
    os.environ["BRIDRA_IOS_SIMULATOR_DEVICE"] = device
    started = time.monotonic()
    try:
        for attempt in range(1 if args.phase == "cold" else 2):
            name = f"positive-{attempt + 1}"
            if attempt and args.reset_replay:
                experiment.run("reset-replay-device", ["xcrun", "simctl", "erase", device], 120)
            if args.runner == "flutter":
                command = ["make", "ios-simulator-smoke", f"DEVICE={device}"]
            else:
                command = [sys.executable, "tool/ios_xctest.py", "--output", str(output / name),
                           "--build-directory", str(root / "build/ios-experiment/xctest-build")]
                if attempt:
                    command.append("--reuse-build")
            experiment.run(name, command, 720)
            results.append({"attempt": name, "success": True,
                            "reusedXCTestArtifact": args.runner == "xctest" and attempt > 0})
        success = True
    finally:
        report = {"success": success, "runner": args.runner, "phase": args.phase,
                  "commit": subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip(),
                  "wallSeconds": round(time.monotonic() - started, 3), "attempts": results,
                  "stages": experiment.stages,
                  "imageVersion": os.environ.get("ImageVersion"),
                  "xcode": subprocess.check_output(["xcodebuild", "-version"], text=True),
                  "cacheNamespace": os.environ.get("CACHE_NAMESPACE")}
        report.update(device=device, resetReplay=args.reset_replay)
        report.update(hosted=os.environ.get("GITHUB_ACTIONS") == "true",
                      controlledDependencies=bool(os.environ.get("CACHE_NAMESPACE")))
        (output / "benchmark.json").write_text(json.dumps(report, indent=2) + "\n")


if __name__ == "__main__":
    main()
