"""Overlap existing Simulator startup with SDK setup on ephemeral GitHub runners."""

import argparse
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import time


def state_path(path, environment):
    if environment.get("GITHUB_ACTIONS") != "true" or environment.get("RUNNER_ENVIRONMENT") != "github-hosted":
        raise ValueError("Simulator preparation is limited to ephemeral GitHub-hosted runners")
    root = Path(environment["RUNNER_TEMP"]).resolve()
    path = Path(path).resolve()
    if root not in path.parents:
        raise ValueError("Simulator state must be inside RUNNER_TEMP")
    return path


def save(path, value):
    temporary = path.with_suffix(".tmp")
    temporary.write_text(json.dumps(value, indent=2) + "\n")
    temporary.replace(path)


def validate_state(state):
    if (not isinstance(state, dict) or state.get("schema") != 1
            or type(state.get("owned")) is not bool or not isinstance(state.get("device"), str)
            or not re.fullmatch(r"[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}", state["device"])):
        raise ValueError("invalid Simulator preparation identity")
    return state


def load(path):
    return validate_state(json.loads(path.read_text()))


def command(*arguments, timeout=20, check=True):
    return subprocess.run(["xcrun", "simctl", *arguments], check=check,
                          timeout=timeout, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)


def discover(path, timeout=120):
    if path.exists():
        raise ValueError("refusing to replace existing Simulator ownership")
    # A fresh hosted runner initializes CoreSimulator on its first query.
    devices = json.loads(command("list", "devices", "available", "--json", timeout=timeout).stdout)["devices"]
    candidates = [device for group in devices.values() for device in group
                  if device.get("isAvailable") is True and device.get("name", "").startswith("iPhone")]
    if not candidates:
        raise ValueError("no available iPhone Simulator")
    device = next((d for d in candidates if d["state"] == "Booted"), candidates[0])
    state = validate_state({"schema": 1, "device": device["udid"], "owned": device["state"] != "Booted"})
    # Validate the UUID before writing an executable worker's input.
    path.parent.mkdir(parents=True, exist_ok=True)
    save(path, state)
    return state


def start(path):
    try:
        state = discover(path, timeout=20)
    except subprocess.TimeoutExpired:
        # Only this optional probe may defer work; smoke still requires strict
        # discovery/readiness and all transport assertions within its retry bound.
        path.parent.mkdir(parents=True, exist_ok=True)
        save(path.with_suffix(".prestart.json"), {"status": "deferred", "reason": "discovery-timeout",
                                                "timeoutSeconds": 20})
        print("Optional Simulator discovery timed out; strict initialization deferred to smoke.", flush=True)
        return None
    if state["owned"]:
        with path.with_suffix(".log").open("w") as log:
            worker = subprocess.Popen(
                [sys.executable, str(Path(__file__).resolve()), "_boot", "--state", str(path)],
                stdin=subprocess.DEVNULL, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
        state["worker_pid"] = worker.pid
        save(path, state)
    return state["device"]


def boot(path):
    state = load(path)
    if not state["owned"]:
        raise ValueError("refusing to boot a borrowed Simulator")
    try:
        result = command("boot", state["device"], timeout=120, check=False)
        print(result.stdout, end="", flush=True)
        outcome = {"exitCode": result.returncode}
    except subprocess.TimeoutExpired:
        outcome = {"exitCode": 124}
    # The worker never edits ownership, so a concurrent ready/finish cannot lose it.
    save(path.with_suffix(".result.json"), {**outcome, "device": state["device"], "workerPid": os.getpid()})


def matching_worker(path, pid):
    if type(pid) is not int or pid <= 1:
        return False
    result = subprocess.run(["ps", "-ww", "-p", str(pid), "-o", "command="],
                            stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True, timeout=5)
    expected = f"{Path(__file__).resolve()} _boot --state {path}"
    return result.returncode == 0 and expected in result.stdout


def stop_worker(path, state):
    pid = state.get("worker_pid")
    if type(pid) is not int or pid <= 1:
        return
    if not matching_worker(path, pid):
        # An orphan or reused PID is never a signal target. Refuse to launch or
        # reset a device while an unverified process group may still be booting it.
        if not wait_group(pid, 2):
            raise ValueError("cannot verify a live preparation process group")
        return
    try:
        if os.getpgid(pid) != pid:
            raise ValueError("preparation worker lost its owned process group")
        os.killpg(pid, signal.SIGTERM)
        # The worker can exit before its simctl child; wait for the entire group.
        if not wait_group(pid, 5):
            os.killpg(pid, signal.SIGKILL)
            if not wait_group(pid, 2):
                raise ValueError("owned preparation process group did not stop")
    except ProcessLookupError:
        if not wait_group(pid, 2):
            raise ValueError("preparation leader exited with a live process group")


def wait_group(pid, seconds):
    until = time.monotonic() + seconds
    while True:
        try:
            os.killpg(pid, 0)
        except ProcessLookupError:
            return True
        if time.monotonic() >= until:
            return False
        time.sleep(0.1)


def wait_for_boot(path, state):
    result_path = path.with_suffix(".result.json")
    deadline = time.monotonic() + 120
    while not result_path.exists():
        if time.monotonic() >= deadline:
            stop_worker(path, state)
            raise subprocess.TimeoutExpired("Simulator preparation worker", 120)
        if not matching_worker(path, state["worker_pid"]) and not result_path.exists():
            raise ValueError("Simulator preparation worker exited without a result")
        time.sleep(0.25)
    result = json.loads(result_path.read_text())
    if (not isinstance(result, dict) or type(result.get("exitCode")) is not int
            or result.get("device") != state["device"]
            or type(result.get("workerPid")) is not int or result["workerPid"] != state["worker_pid"]):
        raise ValueError("invalid Simulator boot result identity")
    stop_worker(path, state)
    return result["exitCode"]


def ready(path):
    state = load(path) if path.exists() else discover(path)
    boot_required = state["owned"]
    if "worker_pid" in state:
        if not state["owned"]:
            raise ValueError("borrowed Simulator cannot own a boot worker")
        # Join the one boot actor before readiness can request any fallback boot.
        boot_required = wait_for_boot(path, state) != 0
        state.pop("worker_pid")
        save(path, state)
    arguments = ["bootstatus", state["device"]]
    if boot_required:
        arguments.append("-b")
    result = command(*arguments, timeout=180)
    print(result.stdout, end="", flush=True)


def reset(path):
    if not path.exists():
        # Discovery failed before any device was selected or booted. The retry
        # performs strict discovery again; there is no device to erase yet.
        return
    state = load(path)
    stop_worker(path, state)
    state["owned"] = True
    state.pop("worker_pid", None)
    save(path, state)
    command("shutdown", state["device"], check=False)
    command("erase", state["device"], timeout=60)


def finish(path):
    if not path.exists():
        return
    state = load(path)
    try:
        stop_worker(path, state)
    finally:
        if state["owned"]:
            result = command("shutdown", state["device"], check=False)
            # A timed-out/failed shutdown must not silently claim cleanup succeeded.
            if result.returncode != 0:
                devices = json.loads(command("list", "devices", "available", "--json").stdout)["devices"]
                selected = [d for group in devices.values() for d in group if d["udid"] == state["device"]]
                if len(selected) != 1 or selected[0]["state"] != "Shutdown":
                    raise ValueError("owned Simulator did not shut down")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("start", "ready", "device", "reset", "finish", "_boot"))
    parser.add_argument("--state", required=True)
    args = parser.parse_args()
    path = state_path(args.state, os.environ)
    if args.command == "start":
        device = start(path)
        with open(os.environ["GITHUB_OUTPUT"], "a") as output:
            output.write(f"device={device or ''}\n")
        if device:
            print(f"Preparing existing iPhone Simulator {device} in parallel with SDK setup.")
    elif args.command == "device":
        print(load(path)["device"])
    else:
        {"ready": ready, "reset": reset, "finish": finish, "_boot": boot}[args.command](path)


if __name__ == "__main__":
    main()
