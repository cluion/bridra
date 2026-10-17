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


def start(path):
    if path.exists():
        raise ValueError("refusing to replace existing Simulator ownership")
    devices = json.loads(command("list", "devices", "available", "--json").stdout)["devices"]
    candidates = [device for group in devices.values() for device in group
                  if device.get("isAvailable") is True and device.get("name", "").startswith("iPhone")]
    if not candidates:
        raise ValueError("no available iPhone Simulator")
    device = next((d for d in candidates if d["state"] == "Booted"), candidates[0])
    state = validate_state({"schema": 1, "device": device["udid"], "owned": device["state"] != "Booted"})
    # Validate the UUID before writing an executable worker's input.
    path.parent.mkdir(parents=True, exist_ok=True)
    save(path, state)
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
    try:
        result = command("boot", state["device"], timeout=120, check=False)
        print(result.stdout, end="", flush=True)
        outcome = {"exitCode": result.returncode}
    except subprocess.TimeoutExpired:
        outcome = {"exitCode": 124}
    # The worker never edits ownership, so a concurrent ready/finish cannot lose it.
    save(path.with_suffix(".result.json"), outcome)


def matching_worker(path, pid):
    if type(pid) is not int or pid <= 1:
        return False
    result = subprocess.run(["ps", "-p", str(pid), "-o", "command="],
                            stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True, timeout=5)
    expected = f"{Path(__file__).resolve()} _boot --state {path}"
    return result.returncode == 0 and expected in result.stdout


def stop_worker(path, state):
    pid = state.get("worker_pid")
    if not matching_worker(path, pid):
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
        pass


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


def ready(path):
    state = load(path)
    # -b also boots after a failed optional prestart; keep this inside the smoke's
    # retry boundary rather than treating SDK preparation as transport acceptance.
    result = command("bootstatus", state["device"], "-b", timeout=180)
    print(result.stdout, end="", flush=True)


def reset(path):
    state = load(path)
    stop_worker(path, state)
    command("shutdown", state["device"], check=False)
    command("erase", state["device"], timeout=60)
    state["owned"] = True
    state.pop("worker_pid", None)
    save(path, state)


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
    parser.add_argument("command", choices=("start", "ready", "reset", "finish", "_boot"))
    parser.add_argument("--state", required=True)
    args = parser.parse_args()
    path = state_path(args.state, os.environ)
    if args.command == "start":
        device = start(path)
        with open(os.environ["GITHUB_OUTPUT"], "a") as output:
            output.write(f"device={device}\n")
        print(f"Preparing existing iPhone Simulator {device} in parallel with SDK setup.")
    else:
        {"ready": ready, "reset": reset, "finish": finish, "_boot": boot}[args.command](path)


if __name__ == "__main__":
    main()
