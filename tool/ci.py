"""Select PR checks and validate the boundaries used by GitHub Actions."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess

from ci_fvm import VERSION as FVM_VERSION


ANDROID_CMAKE_VERSION = "3.22.1"
SCOPES = ("core", "linux", "android", "web", "macos", "ios", "windows",
          "android_emulator", "ios_simulator")
PLATFORMS = ("linux", "android", "web", "macos", "ios", "windows")
GROUPS = {
    "core": ("core-static", "core-go", "core-flutter"),
    "ios": ("ios-watchdog", "ios-simulator", "ios-build"),
    "windows": ("windows-verify", "windows-bundle"),
    "macos": ("macos-lifecycle", "macos-bundle"),
}
TARGETS = [(system, arch) for system in ("darwin", "linux", "windows")
           for arch in ("amd64", "arm64")]


def plan(paths):
    selected = set()
    for path in paths:
        if path.endswith(".md") and ("/" not in path or path.startswith("docs/")):
            continue
        template_prefix = "backend/projecttemplate/templates/"
        if path.startswith(template_prefix):
            selected.add("core")
            selected.update(key for key, enabled in plan([path[len(template_prefix):]]).items() if enabled)
            continue
        platform = path.split("/", 1)[0]
        if platform in PLATFORMS:
            selected.add(platform)
            if platform in ("android", "ios"):
                selected.add(platform + ("_emulator" if platform == "android" else "_simulator"))
        elif path.startswith("tool/"):
            name = path.split("/", 1)[1]
            if name.startswith(("ios_", "gomobile/")):
                selected.update(("ios", "ios_simulator"))
            elif name.startswith(("linux_", "macos_", "android_", "windows.")):
                selected.add(name.split("_", 1)[0].split(".", 1)[0])
                if name.startswith("android_"):
                    selected.add("android_emulator")
            else:
                selected.update(SCOPES)
        elif path.startswith("lib/ui/window/"):
            selected.update(("core", "linux", "macos", "windows"))
        elif path.startswith("integration_test/android_"):
            selected.update(("core", "android", "android_emulator"))
        elif path.startswith("integration_test/ios_"):
            selected.update(("core", "ios", "ios_simulator"))
        elif path.startswith("backend/") and path.endswith(("_windows.go", "_windows_test.go")):
            selected.update(("core", "windows"))
        elif path.startswith("backend/") and path.endswith(("_darwin.go", "_darwin_test.go")):
            selected.update(("core", "macos"))
        elif path.startswith("backend/") and path.endswith(("_linux.go", "_linux_test.go")):
            selected.update(("core", "linux"))
        elif path.startswith(("backend/framework/", "backend/cmd/sidecar/", "backend/cmd/server/",
                              "packages/bridra_flutter/lib/", "schema/")) and not path.endswith("_test.go"):
            # Runtime/wire changes keep real transport checks before merge.
            selected.update(SCOPES)
        elif (path.startswith(("lib/", "test/", "assets/", "packages/bridra_flutter/test/"))
              or (path.startswith("backend/") and path.endswith(".go"))
              or path in ("VERSION", ".bridra/project.json")):
            # Representative desktop coverage for application, CLI and test work;
            # the exact tagged source still builds every target before publication.
            selected.update(("core", "linux"))
        else:
            # SDK/dependency/build configuration and unknown paths remain conservative.
            selected.update(SCOPES)
    return {scope: scope in selected for scope in SCOPES}


def validate_plan(scope):
    if set(scope) != set(SCOPES) or any(type(value) is not bool for value in scope.values()):
        raise ValueError("missing or invalid check plan")


def pinned_flutter(root):
    version = json.loads((Path(root) / ".fvmrc").read_text()).get("flutter")
    if not isinstance(version, str) or not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?", version):
        raise ValueError("CI requires an exact pinned Flutter version")
    return version


def cache_environment(root, environment):
    version = pinned_flutter(root)
    profiles = {
        "Linux": ("linux-android-web", "--linux --android --web"),
        "macOS": ("macos-ios", "--macos --ios"),
        "Windows": ("windows-temp-v1", "--windows"),
    }
    profile, flags = profiles[environment["RUNNER_OS"]]
    cache = Path(environment["RUNNER_TOOL_CACHE"])
    if environment["RUNNER_OS"] == "Windows":
        cache = Path(environment["RUNNER_TEMP"]) / "bridra-dependencies"
    values = {"FVM_CACHE_PATH": (cache / "bridra-fvm").as_posix(),
              "PUB_CACHE": (cache / "bridra-pub").as_posix()}
    with open(environment["GITHUB_ENV"], "a") as output:
        for key, value in values.items():
            output.write(f"{key}={value}\n")
    with open(environment["GITHUB_OUTPUT"], "a") as output:
        output.write(f'version={version}\nfvm={values["FVM_CACHE_PATH"]}\npub={values["PUB_CACHE"]}\n')
        output.write(f'profile={profile}\nprecache_flags={flags}\n')
        output.write(f'fvm_version={FVM_VERSION}\n')


def windows_environment(environment):
    if (environment.get("RUNNER_OS") != "Windows" or environment.get("GITHUB_ACTIONS") != "true"
            or environment.get("RUNNER_ENVIRONMENT") != "github-hosted"):
        raise ValueError("Windows scratch configuration requires an ephemeral hosted Windows runner")
    scratch = Path(environment["RUNNER_TEMP"]) / "bridra-work"
    locations = {"GOCACHE": scratch / "go-build", "GOMODCACHE": scratch / "go-mod",
                 "GOTMPDIR": scratch / "tmp", "TEMP": scratch / "tmp", "TMP": scratch / "tmp"}
    for directory in locations.values():
        directory.mkdir(parents=True, exist_ok=True)
    with open(environment["GITHUB_ENV"], "a") as output:
        for name, directory in locations.items():
            output.write(f"{name}={directory.as_posix()}\n")


def android_native_environment(environment):
    sdk = Path(environment.get("ANDROID_HOME") or environment["ANDROID_SDK_ROOT"])
    manager = sdk / "cmdline-tools/latest/bin/sdkmanager"
    extension = Path(environment["FLUTTER_ROOT"]) / "packages/flutter_tools/gradle/src/main/kotlin/FlutterExtension.kt"
    match = re.search(r'\bval\s+ndkVersion\s*:\s*String\s*=\s*"([0-9]+\.[0-9]+\.[0-9]+)"',
                      extension.read_text())
    if (not match or not sdk.is_dir() or not sdk.is_absolute()
            or not manager.is_file() or not os.access(manager, os.X_OK)):
        raise ValueError("expected the pinned Flutter NDK and an installed Android SDK")
    values = {"ndk": match[1], "cmake": ANDROID_CMAKE_VERSION,
              "ndk_path": (sdk / "ndk" / match[1]).as_posix(),
              "cmake_path": (sdk / "cmake" / ANDROID_CMAKE_VERSION).as_posix(),
              "sdkmanager_path": manager.as_posix()}
    with open(environment["GITHUB_OUTPUT"], "a") as output:
        for name, value in values.items():
            output.write(f"{name}={value}\n")


def flutter_environment(root, environment):
    version = pinned_flutter(root)
    versions = json.loads(subprocess.check_output(["fvm", "api", "list"], text=True))["versions"]
    matches = [item for item in versions if item.get("name") == version]
    if len(matches) != 1:
        raise ValueError("expected one installed SDK matching the pinned Flutter version")
    sdk = Path(matches[0]["directory"])
    dart = sdk / "bin/cache/dart-sdk/bin" / ("dart.exe" if os.name == "nt" else "dart")
    if not dart.is_file() or (os.name != "nt" and not os.access(dart, os.X_OK)):
        raise ValueError(f"pinned Dart SDK not found: {dart}")
    with open(environment["GITHUB_ENV"], "a") as output:
        output.write(f"FLUTTER_ROOT={sdk.as_posix()}\n")
    with open(environment["GITHUB_PATH"], "a") as output:
        output.write(f"{sdk.as_posix()}/bin\n{dart.parent.as_posix()}\n")


def prepare_gradle_wrapper(root, environment):
    # Flutter generates these ignored files during a build, not a plain pub get.
    # Warm the committed distribution in scratch space without building the app.
    source = Path(environment["FLUTTER_ROOT"]) / "bin/cache/artifacts/gradle_wrapper"
    destination = Path(environment["RUNNER_TEMP"]) / "bridra-gradle-warm"
    for relative in ("gradlew", "gradle/wrapper/gradle-wrapper.jar"):
        target = destination / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source / relative, target)
    shutil.copyfile(Path(root) / "android/gradle/wrapper/gradle-wrapper.properties",
                    destination / "gradle/wrapper/gradle-wrapper.properties")
    (destination / "gradlew").chmod(0o755)


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
    validate_plan(scope)
    result = needs.get("checks", {}).get("result")
    allowed = {"success"} if any(scope.values()) else {"skipped"}
    if result not in allowed:
        raise ValueError(f"required checks did not succeed: {result}")


def job_requirements(scope, full):
    validate_plan(scope)
    expected = {
        "core": full or scope["core"],
        "linux": full or scope["linux"],
        "android-web": full or scope["android"] or scope["web"],
        "android-emulator": full or scope["android_emulator"],
        "macos": full or scope["macos"],
        "ios": full or scope["ios"] or scope["ios_simulator"],
        "windows": full or scope["windows"],
        "release-package": full,
    }
    for group, jobs in GROUPS.items():
        expected.update({job: expected[group] for job in jobs})
    expected["ios-build"] = full or scope["ios"]
    expected["ios-watchdog"] = expected["ios-simulator"] = full or scope["ios_simulator"]
    return expected


VERIFICATION_JOBS = tuple(job_requirements({key: False for key in SCOPES}, False))


def check_required_results(expected, needs):
    if set(needs) != set(expected):
        raise ValueError("missing verification job result")
    for job, required in expected.items():
        result = needs[job].get("result")
        if result != ("success" if required else "skipped"):
            raise ValueError(f"unexpected verification job result: {job}={result}")


def check_results(scope, full, needs):
    check_required_results(job_requirements(scope, full), needs)


def check_group(group, scope, full, needs):
    expected = job_requirements(scope, full)
    check_required_results({job: expected[job] for job in GROUPS[group]}, needs)


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
    group = commands.add_parser("group")
    group.add_argument("--group", choices=tuple(GROUPS), required=True)
    group.add_argument("--plan", required=True)
    group.add_argument("--full", required=True, choices=("true", "false"))
    group.add_argument("--needs", required=True)
    artifacts = commands.add_parser("artifacts")
    artifacts.add_argument("--directory", required=True)
    artifacts.add_argument("--version", required=True)
    artifacts.add_argument("--commit", required=True)
    for name in ("cache-env", "flutter-env", "gradle-wrapper", "windows-env", "android-native-env"):
        setup = commands.add_parser(name)
        setup.add_argument("--root", default=".")
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
        elif args.command in ("results", "group"):
            scope = json.loads(args.plan)
            full = args.full == "true"
            # Tag callers intentionally omit a PR plan; full mode still requires every job.
            if full and scope == {}:
                scope = {key: False for key in SCOPES}
            if args.command == "group":
                check_group(args.group, scope, full, json.loads(args.needs))
            else:
                check_results(scope, full, json.loads(args.needs))
            print("Every selected verification job succeeded.")
        elif args.command == "artifacts":
            verify_artifacts(args.directory, args.version, args.commit)
            print("Release files match the verified version, source SHA and digests.")
        elif args.command == "cache-env":
            cache_environment(args.root, os.environ)
        elif args.command == "flutter-env":
            flutter_environment(args.root, os.environ)
        elif args.command == "windows-env":
            windows_environment(os.environ)
        elif args.command == "android-native-env":
            android_native_environment(os.environ)
        else:
            prepare_gradle_wrapper(args.root, os.environ)
    except (ValueError, KeyError, OSError, subprocess.CalledProcessError) as error:
        parser.exit(1, f"CI validation failed: {error}\n")


if __name__ == "__main__":
    main()
