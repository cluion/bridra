"""Install checksum-pinned standalone FVM on Windows x64 CI."""

import hashlib
import io
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import urllib.request
import zipfile


VERSION = "4.1.2"
WINDOWS_ARCHIVES = {
    "X64": "9a18b4daac98dac3c3230ff67ccc644d5a7875d1fc09fb7d848cb2900b9478b8",
}
MAX_ARCHIVE_BYTES = 32 * 1024 * 1024
MAX_EXTRACTED_BYTES = 128 * 1024 * 1024
MEMBERS = ("fvm/fvm.exe", "fvm/src/LICENSE")


def install(environment):
    if environment.get("RUNNER_OS") != "Windows":
        raise ValueError("standalone Windows FVM installer requires a Windows runner")
    architecture = environment.get("RUNNER_ARCH")
    if architecture not in WINDOWS_ARCHIVES:
        raise ValueError("unsupported Windows runner architecture")
    name = f"fvm-{VERSION}-windows-{architecture.lower()}.zip"
    url = f"https://github.com/btwld/fvm/releases/download/{VERSION}/{name}"
    with urllib.request.urlopen(url, timeout=30) as response:
        blob = response.read(MAX_ARCHIVE_BYTES + 1)
    if len(blob) > MAX_ARCHIVE_BYTES:
        raise ValueError("FVM archive exceeds download limit")
    if hashlib.sha256(blob).hexdigest() != WINDOWS_ARCHIVES[architecture]:
        raise ValueError("FVM archive SHA-256 mismatch")
    with zipfile.ZipFile(io.BytesIO(blob)) as archive:
        if sorted(archive.namelist()) != sorted(MEMBERS):
            raise ValueError("unexpected FVM archive contents")
        if sum(info.file_size for info in archive.infolist()) > MAX_EXTRACTED_BYTES:
            raise ValueError("FVM archive exceeds extraction limit")
        # Write only the two fixed member paths, never arbitrary ZIP destinations.
        files = {name: archive.read(name) for name in MEMBERS}
    destination = Path(tempfile.mkdtemp(prefix="bridra-fvm-", dir=environment["RUNNER_TEMP"]))
    try:
        for name, content in files.items():
            target = destination / name
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(content)
        executable = destination / "fvm/fvm.exe"
        version = subprocess.check_output([str(executable), "--version"], text=True, timeout=15).strip()
        if version != VERSION:
            raise ValueError("installed FVM version does not match the pinned version")
        with open(environment["GITHUB_PATH"], "a") as output:
            output.write(str(executable.parent) + "\n")
    except BaseException:
        shutil.rmtree(destination)
        raise
    print(f"Installed SHA-256-verified standalone FVM {version} ({architecture}, {len(blob)} bytes).")


if __name__ == "__main__":
    install(os.environ)
