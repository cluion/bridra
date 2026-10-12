import hashlib
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import ci


class CheckPlanTests(unittest.TestCase):
    def test_only_documentation_needs_no_program_checks(self):
        self.assertFalse(any(ci.plan(["README.md", "docs/RELEASING.md"]).values()))

    def test_native_change_selects_only_affected_platform(self):
        for platform in ("linux", "android", "web", "macos", "ios", "windows"):
            with self.subTest(platform=platform):
                selected = ci.plan([platform + "/runner/source", "docs/GUIDE.md"])
                self.assertEqual([key for key, value in selected.items() if value], [platform])

    def test_platform_tools_select_their_platform(self):
        for path, platform in (("tool/windows.ps1", "windows"),
                               ("tool/macos_smoke.sh", "macos"),
                               ("tool/ios_embedded_core_harness/main.swift", "ios"),
                               ("tool/gomobile/go.mod", "ios")):
            with self.subTest(path=path):
                self.assertEqual(ci.plan([path]), {key: key == platform for key in ci.SCOPES})

    def test_shared_and_unknown_changes_require_all_checks(self):
        for path in ("backend/framework/server.go", "packages/bridra_flutter/lib/bridra.dart",
                     "schema/bridra.json", "Makefile", ".fvmrc", "pubspec.lock",
                     ".github/workflows/verify.yml", "tool/ci.py", "unknown.data"):
            with self.subTest(path=path):
                self.assertTrue(all(ci.plan([path]).values()))

    def test_rename_keeps_old_and_new_platform_scopes(self):
        with patch("ci.subprocess.check_output", return_value=b"windows/old\0linux/new\0") as run:
            selected = ci.plan(ci.changed_paths("a" * 40, "b" * 40))
        self.assertTrue(selected["windows"])
        self.assertTrue(selected["linux"])
        self.assertIn("--no-renames", run.call_args[0][0])

    def test_changed_paths_preserve_newlines(self):
        with patch("ci.subprocess.check_output", return_value=b"linux/new\nfile\0"):
            self.assertEqual(ci.changed_paths("a" * 40, "b" * 40), ["linux/new\nfile"])

    def test_non_immutable_refs_fail_before_git(self):
        with patch("ci.subprocess.check_output") as run:
            with self.assertRaises(ValueError):
                ci.changed_paths("main", "b" * 40)
            run.assert_not_called()


class GateTests(unittest.TestCase):
    def needs(self, changes="success", checks="success"):
        return {"changes": {"result": changes}, "checks": {"result": checks}}

    def test_successful_applicable_checks_pass(self):
        ci.check_gate(ci.plan(["windows/runner/main.cpp"]), self.needs())

    def test_only_intentional_documentation_skip_passes(self):
        ci.check_gate(ci.plan(["README.md"]), self.needs(checks="skipped"))
        with self.assertRaises(ValueError):
            ci.check_gate(ci.plan(["windows/runner/main.cpp"]), self.needs(checks="skipped"))

    def test_failure_cancellation_and_missing_results_block(self):
        for stage in ("changes", "checks"):
            for result in ("failure", "cancelled", "skipped", None):
                with self.subTest(stage=stage, result=result):
                    needs = self.needs()
                    needs[stage]["result"] = result
                    with self.assertRaises(ValueError):
                        ci.check_gate(ci.plan(["Makefile"]), needs)

    def test_incomplete_or_non_boolean_plan_blocks(self):
        for scope in ({}, {key: "false" for key in ci.SCOPES}):
            with self.assertRaises(ValueError):
                ci.check_gate(scope, self.needs(checks="skipped"))


class VerificationResultsTests(unittest.TestCase):
    def test_selected_platform_passes_with_only_intentional_skips(self):
        needs = {key: {"result": "skipped"} for key in
                 ("linux-android-web", "android-emulator", "macos-ios", "windows")}
        needs["windows"]["result"] = "success"
        ci.check_results(ci.plan(["windows/runner/main.cpp"]), False, needs)

    def test_full_verification_rejects_skipped_failed_or_missing_job(self):
        jobs = ("linux-android-web", "android-emulator", "macos-ios", "windows")
        for job in jobs:
            for result in ("skipped", "failure", "cancelled", None):
                with self.subTest(job=job, result=result):
                    needs = {key: {"result": "success"} for key in jobs}
                    needs[job]["result"] = result
                    with self.assertRaises(ValueError):
                        ci.check_results(ci.plan([]), True, needs)
        with self.assertRaises(ValueError):
            ci.check_results(ci.plan([]), True, {})


class ReleaseArtifactTests(unittest.TestCase):
    version = "0.20.1"
    commit = "a" * 40

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        self.manifest = {
            "schemaVersion": 3, "product": "bridra", "license": "MIT",
            "version": self.version, "tag": "backend/v" + self.version, "commit": self.commit,
            "goModule": "github.com/cluion/bridra/backend",
            "cliInstallPath": "github.com/cluion/bridra/backend/cmd/bridra",
            "artifacts": [],
        }
        for system, arch in ci.TARGETS:
            extension = "zip" if system == "windows" else "tar.gz"
            name = f"bridra_{self.version}_{system}_{arch}.{extension}"
            record = self.write_record(name)
            record.update(goos=system, goarch=arch, archive=record.pop("file"))
            self.manifest["artifacts"].append(record)
        self.manifest["sbom"] = self.write_record(f"bridra_{self.version}_cli.spdx.json")
        self.manifest["sbom"].update(format="SPDX-2.3", predicateType="https://spdx.dev/Document/v2.3")
        self.save_manifest()
        self.sums = "".join(f'{item["sha256"]}  {item["archive"]}\n' for item in self.manifest["artifacts"])
        (self.directory / "SHA256SUMS").write_text(self.sums)

    def write_record(self, name):
        data = name.encode()
        (self.directory / name).write_bytes(data)
        return {"file": name, "sha256": hashlib.sha256(data).hexdigest(), "size": len(data)}

    def save_manifest(self):
        (self.directory / "manifest.json").write_text(json.dumps(self.manifest))

    def verify(self):
        ci.verify_artifacts(self.directory, self.version, self.commit)

    def test_complete_candidate_matches_exact_source(self):
        self.verify()

    def test_wrong_version_source_or_license_blocks(self):
        for key, value in (("commit", "b" * 40), ("version", "0.20.0"), ("license", "unknown")):
            with self.subTest(key=key):
                original = self.manifest[key]
                self.manifest[key] = value
                self.save_manifest()
                with self.assertRaises(ValueError):
                    self.verify()
                self.manifest[key] = original

    def test_tampered_archive_and_sbom_block(self):
        for name in (self.manifest["artifacts"][0]["archive"], self.manifest["sbom"]["file"]):
            with self.subTest(name=name):
                path = self.directory / name
                original = path.read_bytes()
                path.write_bytes(b"changed")
                with self.assertRaises(ValueError):
                    self.verify()
                path.write_bytes(original)

    def test_extra_file_blocks_publication(self):
        (self.directory / "unexpected.txt").write_text("extra")
        with self.assertRaises(ValueError):
            self.verify()

    def test_missing_archive_blocks_publication(self):
        (self.directory / self.manifest["artifacts"][0]["archive"]).unlink()
        with self.assertRaises(ValueError):
            self.verify()

    def test_duplicate_target_blocks_publication(self):
        self.manifest["artifacts"][1] = self.manifest["artifacts"][0]
        self.save_manifest()
        with self.assertRaises(ValueError):
            self.verify()

    def test_duplicate_or_missing_checksum_blocks_publication(self):
        for sums in (self.sums + self.sums.splitlines()[0] + "\n", ""):
            with self.subTest(sums=sums):
                (self.directory / "SHA256SUMS").write_text(sums)
                with self.assertRaises(ValueError):
                    self.verify()


if __name__ == "__main__":
    unittest.main()
