import hashlib
import io
import json
import os
from pathlib import Path
import re
import subprocess
import sys
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
                expected = {platform}
                if platform in ("android", "ios"):
                    expected.add("android_emulator" if platform == "android" else "ios_simulator")
                self.assertEqual({key for key, value in selected.items() if value}, expected)

    def test_platform_tools_select_their_platform(self):
        for path, platform in (("tool/windows.ps1", "windows"),
                               ("tool/macos_smoke.sh", "macos"),
                               ("tool/ios_embedded_core_harness/main.swift", "ios"),
                               ("tool/gomobile/go.mod", "ios")):
            with self.subTest(path=path):
                expected = {platform}
                if platform == "ios":
                    expected.add("ios_simulator")
                self.assertEqual(ci.plan([path]), {key: key in expected for key in ci.SCOPES})

    def test_application_cli_and_test_changes_use_core_and_representative_desktop(self):
        for path in ("lib/features/settings/settings_view.dart", "backend/cmd/bridra/upgrade.go",
                     "backend/app/services/greeting.go", "test/widget_test.dart",
                     "packages/bridra_flutter/test/sidecar_client_test.dart",
                     "backend/framework/server_test.go", "VERSION"):
            with self.subTest(path=path):
                self.assertEqual(ci.plan([path]), {key: key in ("core", "linux") for key in ci.SCOPES})

    def test_desktop_adapter_keeps_all_native_desktop_checks(self):
        self.assertEqual(ci.plan(["lib/ui/window/desktop_window.dart"]),
                         {key: key in ("core", "linux", "macos", "windows") for key in ci.SCOPES})

    def test_platform_specific_go_keeps_its_native_runner_and_core(self):
        for suffix, platform in (("darwin", "macos"), ("windows", "windows"), ("linux", "linux")):
            with self.subTest(platform=platform):
                self.assertEqual(ci.plan([f"backend/framework/parent_process_{suffix}.go"]),
                                 {key: key in ("core", platform) for key in ci.SCOPES})

    def test_template_native_paths_keep_core_and_native_checks(self):
        selected = ci.plan(["backend/projecttemplate/templates/windows/runner/main.cpp.tmpl"])
        self.assertEqual(selected, {key: key in ("core", "windows") for key in ci.SCOPES})
        self.assertTrue(all(ci.plan(["backend/projecttemplate/templates/unknown.data"]).values()))

    def test_mobile_integration_changes_keep_real_device_runtime_checks(self):
        for platform, runtime in (("android", "android_emulator"), ("ios", "ios_simulator")):
            with self.subTest(platform=platform):
                selected = ci.plan([f"integration_test/{platform}_http_test.dart"])
                self.assertEqual(selected, {key: key in ("core", platform, runtime) for key in ci.SCOPES})

    def test_shared_and_unknown_changes_require_all_checks(self):
        for path in ("backend/framework/server.go", "packages/bridra_flutter/lib/bridra.dart",
                     "schema/bridra.json", "Makefile", ".fvmrc", "pubspec.lock",
                     "backend/go.mod", "backend/go.sum", "backend/integration/sqljobstore/go.mod",
                     "backend/unknown.data",
                     ".github/workflows/verify.yml", "tool/ci.py", "unknown.data",
                     "android_emulator/not-a-native-directory"):
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
    jobs = ("core", "linux", "android-web", "android-emulator", "macos", "ios", "windows", "release-package")

    def test_selected_platform_passes_with_only_intentional_skips(self):
        needs = {key: {"result": "skipped"} for key in self.jobs}
        needs["windows"]["result"] = "success"
        ci.check_results(ci.plan(["windows/runner/main.cpp"]), False, needs)

    def test_ordinary_ui_change_requires_no_mobile_or_release_job(self):
        needs = {key: {"result": "skipped"} for key in self.jobs}
        for job in ("core", "linux"):
            needs[job]["result"] = "success"
        ci.check_results(ci.plan(["lib/app/starter_app.dart"]), False, needs)
        needs["core"]["result"] = "skipped"
        with self.assertRaises(ValueError):
            ci.check_results(ci.plan(["lib/app/starter_app.dart"]), False, needs)

    def test_critical_pr_runs_all_platforms_without_producing_release_candidate(self):
        needs = {key: {"result": "success"} for key in self.jobs}
        needs["release-package"]["result"] = "skipped"
        ci.check_results(ci.plan(["backend/framework/server.go"]), False, needs)
        needs["android-emulator"]["result"] = "skipped"
        with self.assertRaises(ValueError):
            ci.check_results(ci.plan(["backend/framework/server.go"]), False, needs)

    def test_full_verification_rejects_skipped_failed_or_missing_job(self):
        for job in self.jobs:
            for result in ("skipped", "failure", "cancelled", None):
                with self.subTest(job=job, result=result):
                    needs = {key: {"result": "success"} for key in self.jobs}
                    needs[job]["result"] = result
                    with self.assertRaises(ValueError):
                        ci.check_results(ci.plan([]), True, needs)
            needs = {key: {"result": "success"} for key in self.jobs if key != job}
            with self.assertRaises(ValueError):
                ci.check_results(ci.plan([]), True, needs)
        with self.assertRaises(ValueError):
            ci.check_results(ci.plan([]), True, {})

    def test_full_verification_rejects_malformed_plan_even_if_all_jobs_pass(self):
        needs = {key: {"result": "success"} for key in self.jobs}
        with self.assertRaises(ValueError):
            ci.check_results({}, True, needs)


class SDKEnvironmentTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / ".fvmrc").write_text('{"flutter":"3.44.6"}')
        self.environment = {name: str(self.root / name) for name in
                            ("RUNNER_TOOL_CACHE", "GITHUB_ENV", "GITHUB_OUTPUT", "GITHUB_PATH")}
        self.environment["RUNNER_OS"] = "Linux"

    def test_cache_paths_export_only_dependency_locations(self):
        ci.cache_environment(self.root, self.environment)
        output = Path(self.environment["GITHUB_OUTPUT"]).read_text()
        self.assertIn("version=3.44.6\n", output)
        self.assertIn("/bridra-fvm\n", output)
        self.assertIn("/bridra-pub\n", output)
        env = Path(self.environment["GITHUB_ENV"]).read_text()
        self.assertIn("FVM_CACHE_PATH=", env)
        self.assertIn("PUB_CACHE=", env)

    def test_sdk_cache_profile_requires_all_artifacts_for_each_host(self):
        for host, profile, flags in (
                ("Linux", "linux-android-web", "--linux --android --web"),
                ("macOS", "macos-ios", "--macos --ios"),
                ("Windows", "windows", "--windows")):
            with self.subTest(host=host):
                self.environment["RUNNER_OS"] = host
                Path(self.environment["GITHUB_OUTPUT"]).unlink(missing_ok=True)
                ci.cache_environment(self.root, self.environment)
                output = Path(self.environment["GITHUB_OUTPUT"]).read_text()
                self.assertIn(f"profile={profile}\n", output)
                self.assertIn(f"precache_flags={flags}\n", output)

    def test_unknown_host_fails_before_exporting_a_cache_profile(self):
        self.environment["RUNNER_OS"] = "unknown"
        with self.assertRaises(KeyError):
            ci.cache_environment(self.root, self.environment)
        self.assertFalse(Path(self.environment["GITHUB_ENV"]).exists())

    def test_floating_or_injected_sdk_versions_fail_before_export(self):
        for version in ("stable", "3.44.6\nother=value", None):
            with self.subTest(version=version):
                (self.root / ".fvmrc").write_text(json.dumps({"flutter": version}))
                with self.assertRaises(ValueError):
                    ci.cache_environment(self.root, self.environment)
                self.assertFalse(Path(self.environment["GITHUB_ENV"]).exists())

    def test_flutter_export_resolves_exact_sdk_without_relying_on_project_symlink(self):
        sdk = self.root / "installed-sdk"
        dart = sdk / "bin/cache/dart-sdk/bin" / ("dart.exe" if os.name == "nt" else "dart")
        dart.parent.mkdir(parents=True)
        dart.touch()
        dart.chmod(0o755)
        data = {"versions": [{"name": "another-version", "directory": "wrong"},
                             {"name": "3.44.6", "directory": str(sdk)}]}
        with patch("ci.subprocess.check_output", return_value=json.dumps(data)):
            ci.flutter_environment(self.root, self.environment)
        self.assertEqual(Path(self.environment["GITHUB_ENV"]).read_text(), f"FLUTTER_ROOT={sdk.as_posix()}\n")
        self.assertIn(dart.parent.as_posix(), Path(self.environment["GITHUB_PATH"]).read_text())

    def test_missing_wrong_or_ambiguous_sdk_blocks_export(self):
        for versions in ([], [{"name": "3.44.6", "directory": str(self.root / 'missing')}],
                         [{"name": "3.44.6", "directory": "a"}, {"name": "3.44.6", "directory": "b"}]):
            with self.subTest(versions=versions):
                with patch("ci.subprocess.check_output", return_value=json.dumps({"versions": versions})):
                    with self.assertRaises(ValueError):
                        ci.flutter_environment(self.root, self.environment)
                self.assertFalse(Path(self.environment["GITHUB_ENV"]).exists())

    @unittest.skipIf(os.name == "nt", "Windows executable permissions use a different model")
    def test_non_executable_dart_blocks_sdk_export(self):
        dart = self.root / 'sdk/bin/cache/dart-sdk/bin/dart'
        dart.parent.mkdir(parents=True)
        dart.touch()
        data = {"versions": [{"name": "3.44.6", "directory": str(self.root / 'sdk')}]}
        with patch("ci.subprocess.check_output", return_value=json.dumps(data)):
            with self.assertRaises(ValueError):
                ci.flutter_environment(self.root, self.environment)
        self.assertFalse(Path(self.environment["GITHUB_ENV"]).exists())

    def test_gradle_warmup_works_without_ignored_project_wrapper_files(self):
        sdk = self.root / 'sdk/bin/cache/artifacts/gradle_wrapper'
        (sdk / 'gradle/wrapper').mkdir(parents=True)
        (sdk / 'gradlew').write_text('pinned SDK wrapper')
        (sdk / 'gradle/wrapper/gradle-wrapper.jar').write_bytes(b'pinned SDK jar')
        properties = self.root / 'android/gradle/wrapper/gradle-wrapper.properties'
        properties.parent.mkdir(parents=True)
        properties.write_text('distributionUrl=pinned-project-gradle.zip\n')
        environment = {'FLUTTER_ROOT': str(self.root / 'sdk'), 'RUNNER_TEMP': str(self.root / 'scratch')}
        ci.prepare_gradle_wrapper(self.root, environment)
        destination = self.root / 'scratch/bridra-gradle-warm'
        self.assertEqual((destination / 'gradlew').read_text(), 'pinned SDK wrapper')
        self.assertEqual((destination / 'gradle/wrapper/gradle-wrapper.jar').read_bytes(), b'pinned SDK jar')
        self.assertEqual((destination / 'gradle/wrapper/gradle-wrapper.properties').read_bytes(), properties.read_bytes())
        self.assertFalse((self.root / 'android/gradlew').exists())
        self.assertFalse((self.root / 'android/gradle/wrapper/gradle-wrapper.jar').exists())


class WorkflowWiringTests(unittest.TestCase):
    def test_reusable_gate_depends_on_every_verification_and_packaging_job(self):
        path = Path(__file__).resolve().parent.parent / '.github/workflows/checks.yml'
        workflow = path.read_text().split('\njobs:\n', 1)[1]
        jobs = set(re.findall(r'^  ([a-z][a-z0-9-]*):$', workflow, re.MULTILINE))
        needs = re.search(r'^    needs: \[([^]]+)\]$', workflow, re.MULTILINE)
        self.assertIsNotNone(needs)
        self.assertEqual({name.strip() for name in needs[1].split(',')}, jobs - {'check-results'})
        self.assertEqual(jobs - {'check-results'}, set(VerificationResultsTests.jobs))

    def test_pr_requires_its_authoritative_plan_while_full_tag_requires_all_jobs(self):
        empty_jobs = {key: {"result": "skipped"} for key in VerificationResultsTests.jobs}
        for full, needs in ((False, empty_jobs), (True, empty_jobs)):
            with self.subTest(full=full):
                argv = ['ci.py', 'results', '--plan', '{}', '--full', str(full).lower(), '--needs', json.dumps(needs)]
                with patch.object(sys, 'argv', argv), patch('sys.stderr', new_callable=io.StringIO) as stderr:
                    with self.assertRaises(SystemExit) as error:
                        ci.main()
                self.assertEqual(error.exception.code, 1)
                self.assertIn('CI validation failed:', stderr.getvalue())
        jobs = {key: {"result": "success"} for key in VerificationResultsTests.jobs}
        argv = ['ci.py', 'results', '--plan', '{}', '--full', 'true', '--needs', json.dumps(jobs)]
        with patch.object(sys, 'argv', argv), patch('sys.stdout', new_callable=io.StringIO):
            ci.main()


class TestExecutionGraphTests(unittest.TestCase):
    def test_combined_verification_runs_each_suite_once_with_race_and_coverage(self):
        root = Path(__file__).resolve().parent.parent
        commands = subprocess.check_output(["make", "-n", "verify-coverage"], cwd=root, text=True)
        self.assertEqual(commands.count("test -race -covermode=atomic -coverprofile="), 1)
        self.assertEqual(commands.count("flutter test --coverage"), 2)
        self.assertEqual(commands.count("flutter test --platform chrome"), 1)
        self.assertIn("backend/integration/sqljobstore", commands)
        self.assertIn("./tool/coveragecheck --root ..", commands)
        self.assertNotIn("-run '^TestPublic'", commands)


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
