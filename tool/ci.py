"""Select PR checks and validate the boundaries used by GitHub Actions."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess


SCOPES = ("core", "linux", "android", "web", "macos", "ios", "windows")
TARGETS = [(system, arch) for system in ("darwin", "linux", "windows")
           for arch in ("amd64", "arm64")]


def plan(paths):
    selected = set()
    for path in paths:
        if path.endswith(".md") and ("/" not in path or path.startswith("docs/")):
            continue
        platform = path.split("/", 1)[0]
        if platform in SCOPES and platform != "core":
            selected.add(platform)
        elif path.startswith("tool/"):
            name = path.split("/", 1)[1]
            if name.startswith(("ios_", "gomobile/")):
                selected.add("ios")
            elif name.startswith(("linux_", "macos_", "android_", "windows.")):
                selected.add(name.split("_", 1)[0].split(".", 1)[0])
            else:
                selected.update(SCOPES)
        else:
            # Shared code, build configuration and unknown paths can affect every target.
            selected.update(SCOPES)
    return {scope: scope in selected for scope in SCOPES}


def changed_paths(base, head):
    for revision in (base, head):
        if not re.fullmatch(r"(?:[0-9a-f]{40}|[0-9a-f]{64})", revision):
            raise ValueError("expected immutable base and head commit SHAs")
    # Treat a rename as deletion plus addition, so both platform scopes are tested.
    output = subprocess.check_output(
        ["git", "diff", "--no-renames", "--name-only", "-z", base, head, "--"]
    )
    return [os.fsdecode(path) for path in output.split(b"\0") if path]


def check_gate(scope, needs):
    if needs.get("changes", {}).get("result") != "success":
        raise ValueError("change planning did not succeed")
    if set(scope) != set(SCOPES) or any(type(value) is not bool for value in scope.values()):
        raise ValueError("missing or invalid check plan")
    result = needs.get("checks", {}).get("result")
    allowed = {"success"} if any(scope.values()) else {"skipped"}
    if result not in allowed:
        raise ValueError(f"required checks did not succeed: {result}")


def check_results(scope, full, needs):
    expected = {
        "linux-android-web": full or any(scope[key] for key in ("core", "linux", "android", "web")),
        "android-emulator": full or scope["android"],
        "macos-ios": full or scope["macos"] or scope["ios"],
        "windows": full or scope["windows"],
    }
    if set(needs) != set(expected):
        raise ValueError("missing verification job result")
    for job, required in expected.items():
        result = needs[job].get("result")
        if result != ("success" if required else "skipped"):
            raise ValueError(f"unexpected verification job result: {job}={result}")


def verify_artifacts(directory, version, commit):
    directory = Path(directory)
    manifest = json.loads((directory / "manifest.json").read_text())
    expected = {
        "schemaVersion": 3, "product": "bridra", "license": "MIT",
        "version": version, "tag": "backend/v" + version, "commit": commit,
        "goModule": "github.com/cluion/bridra/backend",
        "cliInstallPath": "github.com/cluion/bridra/backend/cmd/bridra",
    }
    for key, value in expected.items():
        if manifest.get(key) != value:
            raise ValueError(f"release manifest mismatch: {key}")
    archives = {
        f"bridra_{version}_{system}_{arch}.{'zip' if system == 'windows' else 'tar.gz'}":
        (system, arch) for system, arch in TARGETS
    }
    artifacts = manifest.get("artifacts", [])
    if len(artifacts) != len(archives):
        raise ValueError("expected all six CLI archives")
    records = {}
    for artifact in artifacts:
        name = artifact["archive"]
        if name not in archives or name in records:
            raise ValueError("unknown or duplicate archive")
        if (artifact.get("goos"), artifact.get("goarch")) != archives[name]:
            raise ValueError("archive target mismatch")
        records[name] = artifact
    sbom = manifest["sbom"]
    sbom_name = f"bridra_{version}_cli.spdx.json"
    if (sbom.get("file") != sbom_name or sbom.get("format") != "SPDX-2.3"
            or sbom.get("predicateType") != "https://spdx.dev/Document/v2.3"):
        raise ValueError("SBOM identity mismatch")
    records[sbom_name] = sbom
    if {path.name for path in directory.iterdir()} != set(records) | {"manifest.json", "SHA256SUMS"}:
        raise ValueError("expected exactly nine release files")
    for name, record in records.items():
        path = directory / name
        if path.is_symlink() or not path.is_file():
            raise ValueError(f"invalid release file: {name}")
        data = path.read_bytes()
        if hashlib.sha256(data).hexdigest() != record["sha256"] or len(data) != record["size"]:
            raise ValueError(f"release file digest or size mismatch: {name}")
    sums = {}
    for line in (directory / "SHA256SUMS").read_text().splitlines():
        match = re.fullmatch(r"([0-9a-f]{64})  (\S+)", line)
        if match is None or match[2] in sums:
            raise ValueError("invalid or duplicate checksum entry")
        sums[match[2]] = match[1]
    if sums != {name: records[name]["sha256"] for name in archives}:
        raise ValueError("checksum file does not match the six manifest archives")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    changes = commands.add_parser("plan")
    changes.add_argument("--base", required=True)
    changes.add_argument("--head", required=True)
    gate = commands.add_parser("gate")
    gate.add_argument("--plan", required=True)
    gate.add_argument("--needs", required=True)
    results = commands.add_parser("results")
    results.add_argument("--plan", required=True)
    results.add_argument("--full", required=True, choices=("true", "false"))
    results.add_argument("--needs", required=True)
    artifacts = commands.add_parser("artifacts")
    artifacts.add_argument("--directory", required=True)
    artifacts.add_argument("--version", required=True)
    artifacts.add_argument("--commit", required=True)
    args = parser.parse_args()
    try:
        if args.command == "plan":
            scope = plan(changed_paths(args.base, args.head))
            output = {"scope": json.dumps(scope), "run_checks": str(any(scope.values())).lower()}
            output.update({key: str(value).lower() for key, value in scope.items()})
            with open(os.environ["GITHUB_OUTPUT"], "a") as handle:
                for key, value in output.items():
                    handle.write(f"{key}={value}\n")
            print(json.dumps(scope, indent=2))
        elif args.command == "gate":
            check_gate(json.loads(args.plan), json.loads(args.needs))
            print("All applicable PR checks passed.")
        elif args.command == "results":
            check_results(json.loads(args.plan), args.full == "true", json.loads(args.needs))
            print("Every selected verification job succeeded.")
        else:
            verify_artifacts(args.directory, args.version, args.commit)
            print("Release files match the verified version, source SHA and digests.")
    except (ValueError, KeyError, OSError, subprocess.CalledProcessError) as error:
        parser.exit(1, f"CI validation failed: {error}\n")


if __name__ == "__main__":
    main()
