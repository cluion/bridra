"""Run independent iOS prerequisites concurrently on ephemeral hosted runners."""

import argparse
from concurrent.futures import ThreadPoolExecutor
import hashlib
import json
import math
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import time
import uuid

import ios_simulator_prepare as simulator

ROOT = Path(__file__).resolve().parent.parent
STAGES = ("xcode-check", "backend-build", "simulator-ready")


def identity():
    head = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True, timeout=10).strip()
    if not re.fullmatch(r"[0-9a-f]{40}", head):
        raise ValueError("invalid preparation source identity")
    return {"head": head, "developerDirectory": os.environ.get("DEVELOPER_DIR", ""), "root": str(ROOT),
            "simulatorRuntime": os.environ.get("BRIDRA_IOS_SIMULATOR_RUNTIME", ""),
            "simulatorName": os.environ.get("BRIDRA_IOS_SIMULATOR_NAME", ""),
            "simulatorBuildJobs": os.environ.get("BRIDRA_IOS_CI_XCODE_JOBS", "")}


def native_state(path):
    return path.with_name("simulator.json")


def load(path):
    state = json.loads(path.read_text())
    if (not isinstance(state, dict) or state.get("schema") != 1
            or type(state.get("pid")) is not int or state["pid"] <= 1
            or not isinstance(state.get("token"), str)
            or not re.fullmatch(r"[0-9a-f]{32}", state["token"])
            or state.get("source") != identity()):
        raise ValueError("invalid or stale iOS prerequisite ownership")
    return state


def begin(path):
    if path.exists() or path.with_suffix(".result.json").exists() or native_state(path).exists():
        raise ValueError("refusing to replace existing iOS preparation")
    path.parent.mkdir(parents=True, exist_ok=True)
    token = uuid.uuid4().hex
    source = identity()
    with path.with_suffix(".worker.log").open("w") as output:
        process = subprocess.Popen(
            [sys.executable, str(Path(__file__).resolve()), "_work", "--state", str(path), "--token", token],
            cwd=ROOT, stdin=subprocess.DEVNULL, stdout=output, stderr=subprocess.STDOUT, start_new_session=True)
    simulator.save(path, {"schema": 1, "pid": process.pid, "token": token, "source": source})
    print("Preparing independent Xcode, Go backend and existing Simulator prerequisites.", flush=True)


def commands(path):
    return {
        "xcode-check": (["make", "macos-check"], 180),
        "backend-build": (["make", "backend-server-build"], 180),
        "simulator-ready": ([sys.executable, str(ROOT / "tool/ios_simulator_prepare.py"),
                             "ready", "--state", str(native_state(path))], 330),
    }


def run_stage(path, stage, arguments, timeout):
    started = time.monotonic()
    with path.with_name(stage + ".log").open("w") as output:
        try:
            status = subprocess.run(arguments, cwd=ROOT, stdout=output, stderr=subprocess.STDOUT,
                                    timeout=timeout).returncode
        except subprocess.TimeoutExpired:
            status = 124
        except OSError as error:
            output.write(str(error) + "\n")
            status = 1
    return {"exit": status, "seconds": round(time.monotonic() - started, 3)}


def work(path, token):
    # The parent records the PID after spawning. The worker never edits ownership.
    deadline = time.monotonic() + 5
    while not path.exists():
        if time.monotonic() >= deadline:
            raise TimeoutError("preparation ownership was not recorded")
        time.sleep(0.05)
    state = load(path)
    if state["token"] != token or state["pid"] != os.getpid():
        raise ValueError("preparation worker identity mismatch")
    with ThreadPoolExecutor(max_workers=3) as executor:
        pending = {name: executor.submit(run_stage, path, name, args, timeout)
                   for name, (args, timeout) in commands(path).items()}
        results = {name: future.result() for name, future in pending.items()}
    server = ROOT / "build/server/bridra_server"
    digest = hashlib.sha256(server.read_bytes()).hexdigest() if all(row["exit"] == 0 for row in results.values()) else None
    simulator.save(path.with_suffix(".result.json"),
                   {"pid": state["pid"], "token": token, "source": state["source"],
                    "stages": results, "backendSHA256": digest})


def matching(path, state):
    process = subprocess.run(["ps", "-ww", "-p", str(state["pid"]), "-o", "command="],
                             stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True, timeout=5)
    expected = f"{Path(__file__).resolve()} _work --state {path} --token {state['token']}"
    return process.returncode == 0 and expected in process.stdout


def stop(path, state):
    pid = state["pid"]
    if not matching(path, state):
        if not simulator.wait_group(pid, 2):
            raise ValueError("cannot verify a live iOS prerequisite process group")
        return
    try:
        if os.getpgid(pid) != pid:
            raise ValueError("iOS prerequisite worker lost its owned process group")
        os.killpg(pid, signal.SIGTERM)
        if not simulator.wait_group(pid, 5):
            os.killpg(pid, signal.SIGKILL)
            if not simulator.wait_group(pid, 2):
                raise ValueError("iOS prerequisite process group did not stop")
    except ProcessLookupError:
        if not simulator.wait_group(pid, 2):
            raise ValueError("iOS prerequisite leader exited with live children")


def result(path, require_success=True):
    state = load(path)
    value = json.loads(path.with_suffix(".result.json").read_text())
    if (not isinstance(value, dict) or any(value.get(key) != state[key] for key in ("pid", "token", "source"))
            or type(value.get("pid")) is not int or not isinstance(value.get("stages"), dict)
            or set(value["stages"]) != set(STAGES)):
        raise ValueError("invalid iOS prerequisite result")
    for stage, row in value["stages"].items():
        if (not isinstance(row, dict) or type(row.get("exit")) is not int
                or type(row.get("seconds")) not in (int, float)
                or not math.isfinite(row["seconds"]) or row["seconds"] < 0):
            raise ValueError("invalid iOS prerequisite stage result")
        if require_success and row["exit"] != 0:
            raise subprocess.CalledProcessError(row["exit"], stage)
    server = ROOT / "build/server/bridra_server"
    if (all(row["exit"] == 0 for row in value["stages"].values())
            and (not server.is_file() or not os.access(server, os.X_OK)
                 or hashlib.sha256(server.read_bytes()).hexdigest() != value.get("backendSHA256"))):
        raise ValueError("prepared backend is missing or changed")
    return value


def ready(path):
    state = load(path)
    deadline = time.monotonic() + 360
    try:
        while not path.with_suffix(".result.json").exists():
            if time.monotonic() >= deadline:
                raise TimeoutError("iOS prerequisites exceeded readiness bound")
            if not matching(path, state) and not path.with_suffix(".result.json").exists():
                raise ValueError("iOS prerequisite worker exited without a result")
            time.sleep(0.5)
    finally:
        stop(path, state)
        for stage in STAGES:
            log = path.with_name(stage + ".log")
            if log.exists():
                print(log.read_text(), end="", flush=True)
    value = result(path, require_success=False)
    for stage, row in value["stages"].items():
        print(f"Bridra iOS {stage} seconds: {row['seconds']:.3f}", flush=True)
        print(f"Bridra iOS {stage} exit: {row['exit']}", flush=True)
    for stage, row in value["stages"].items():
        if row["exit"] != 0:
            raise subprocess.CalledProcessError(row["exit"], stage)


def finish(path, reset=False):
    if path.exists():
        stop(path, load(path))
    if reset:
        simulator.reset(native_state(path))
    else:
        simulator.finish(native_state(path))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("begin", "ready", "verify", "reset", "finish", "_work"))
    parser.add_argument("--state", required=True)
    parser.add_argument("--token")
    parser.add_argument("--device")
    args = parser.parse_args()
    path = simulator.state_path(args.state, os.environ)
    if args.command == "begin":
        begin(path)
    elif args.command == "_work":
        work(path, args.token)
        # Keep the verifiable group leader alive until ready/finish joins it.
        # A timed-out stage may have descendants that must be stopped as a group.
        time.sleep(900)
    elif args.command == "ready":
        ready(path)
    elif args.command == "verify":
        result(path)
        if simulator.load(native_state(path))["device"] != args.device:
            raise ValueError("prepared Simulator does not match the smoke target")
    else:
        finish(path, reset=args.command == "reset")


if __name__ == "__main__":
    main()
