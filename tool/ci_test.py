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
                               ("tool/windows.test.ps1", "windows"),
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
    jobs = ("core-static", "core-go", "core-flutter", "core", "linux", "android-web",
            "android-emulator", "macos-lifecycle", "macos-bundle", "macos", "ios-watchdog", "ios-build", "ios-simulator",
            "ios", "windows-verify", "windows-bundle", "windows", "release-package")

    def test_selected_platform_passes_with_only_intentional_skips(self):
        needs = {key: {"result": "skipped"} for key in self.jobs}
        for job in ("windows-verify", "windows-bundle", "windows"):
            needs[job]["result"] = "success"
        ci.check_results(ci.plan(["windows/runner/main.cpp"]), False, needs)

    def test_ordinary_ui_change_requires_no_mobile_or_release_job(self):
        needs = {key: {"result": "skipped"} for key in self.jobs}
        for job in ("core-static", "core-go", "core-flutter", "core", "linux"):
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

    def test_unrequested_job_or_unknown_result_never_counts_as_an_intentional_skip(self):
        needs = {key: {"result": "skipped"} for key in self.jobs}
        needs["ios-build"]["result"] = "success"
        with self.assertRaises(ValueError):
            ci.check_results(ci.plan([]), False, needs)
        needs["ios-build"]["result"] = "skipped"
        needs["unknown"] = {"result": "success"}
        with self.assertRaises(ValueError):
            ci.check_results(ci.plan([]), False, needs)


class ShardGateTests(unittest.TestCase):
    groups = {"core": ("core-static", "core-go", "core-flutter"),
              "windows": ("windows-verify", "windows-bundle"),
              "macos": ("macos-lifecycle", "macos-bundle"),
              "ios": ("ios-watchdog", "ios-build", "ios-simulator")}

    def test_every_full_shard_failure_cancellation_skip_or_missing_result_blocks(self):
        for group, jobs in self.groups.items():
            passing = {key: {"result": "success"} for key in jobs}
            ci.check_group(group, ci.plan([]), True, passing)
            for job in jobs:
                for result in ("failure", "cancelled", "skipped", None):
                    with self.subTest(group=group, job=job, result=result), self.assertRaises(ValueError):
                        ci.check_group(group, ci.plan([]), True, {**passing, job: {"result": result}})
                with self.subTest(group=group, missing=job), self.assertRaises(ValueError):
                    ci.check_group(group, ci.plan([]), True, {key: value for key, value in passing.items() if key != job})

    def test_ios_build_and_runtime_scopes_have_only_intentional_skips(self):
        for scope in ("ios", "ios_simulator"):
            plan = {key: key == scope for key in ci.SCOPES}
            needs = {job: {"result": "success" if ((job == "ios-build") == (scope == "ios")) else "skipped"}
                     for job in self.groups["ios"]}
            ci.check_group("ios", plan, False, needs)
            all_jobs = {key: {"result": "skipped"} for key in VerificationResultsTests.jobs}
            all_jobs.update(needs)
            all_jobs["ios"] = {"result": "success"}
            ci.check_results(plan, False, all_jobs)
            for job, result in needs.items():
                wrong = "skipped" if result["result"] == "success" else "success"
                with self.subTest(scope=scope, job=job), self.assertRaises(ValueError):
                    ci.check_group("ios", plan, False, {**needs, job: {"result": wrong}})

    def test_group_cannot_hide_unexpected_jobs_or_a_missing_authoritative_plan(self):
        for group, jobs in self.groups.items():
            needs = {job: {"result": "success"} for job in jobs}
            with self.subTest(group=group), self.assertRaises(ValueError):
                ci.check_group(group, ci.plan([]), True, {**needs, "unexpected": {"result": "success"}})
            with self.assertRaises(ValueError):
                ci.check_group(group, {}, False, needs)

    def test_full_tag_group_cli_requires_all_shards_without_a_pr_plan(self):
        for group, jobs in self.groups.items():
            needs = {job: {"result": "success"} for job in jobs}
            argv = ["ci.py", "group", "--group", group, "--plan", "{}", "--full", "true",
                    "--needs", json.dumps(needs)]
            with patch.object(sys, "argv", argv), patch("sys.stdout", new_callable=io.StringIO):
                ci.main()


class SDKEnvironmentTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / ".fvmrc").write_text('{"flutter":"3.44.6"}')
        self.environment = {name: str(self.root / name) for name in
                            ("RUNNER_TOOL_CACHE", "RUNNER_TEMP", "GITHUB_ENV", "GITHUB_OUTPUT", "GITHUB_PATH")}
        self.environment["RUNNER_OS"] = "Linux"

    def test_cache_paths_export_only_dependency_locations(self):
        ci.cache_environment(self.root, self.environment)
        output = Path(self.environment["GITHUB_OUTPUT"]).read_text()
        self.assertIn("version=3.44.6\n", output)
        self.assertIn("/bridra-fvm\n", output)
        self.assertIn("/bridra-pub\n", output)
        self.assertIn("fvm_version=4.1.2\n", output)
        env = Path(self.environment["GITHUB_ENV"]).read_text()
        self.assertIn("FVM_CACHE_PATH=", env)
        self.assertIn("PUB_CACHE=", env)

    def test_sdk_cache_profile_requires_all_artifacts_for_each_host(self):
        for host, profile, flags in (
                ("Linux", "linux-android-web", "--linux --android --web"),
                ("macOS", "macos-ios", "--macos --ios"),
                ("Windows", "windows-temp-v1", "--windows")):
            with self.subTest(host=host):
                self.environment["RUNNER_OS"] = host
                Path(self.environment["GITHUB_OUTPUT"]).unlink(missing_ok=True)
                ci.cache_environment(self.root, self.environment)
                output = Path(self.environment["GITHUB_OUTPUT"]).read_text()
                self.assertIn(f"profile={profile}\n", output)
                self.assertIn(f"precache_flags={flags}\n", output)

    def test_windows_dependency_cache_uses_runner_scratch_instead_of_system_disk(self):
        self.environment["RUNNER_OS"] = "Windows"
        ci.cache_environment(self.root, self.environment)
        env = Path(self.environment["GITHUB_ENV"]).read_text()
        self.assertIn(f"FVM_CACHE_PATH={self.environment['RUNNER_TEMP']}/bridra-dependencies/bridra-fvm\n", env)
        self.assertNotIn(self.environment["RUNNER_TOOL_CACHE"], env)

    def test_windows_build_and_test_scratch_is_owned_by_the_hosted_job(self):
        environment = {**self.environment, "RUNNER_OS": "Windows", "GITHUB_ACTIONS": "true",
                       "RUNNER_ENVIRONMENT": "github-hosted"}
        ci.windows_environment(environment)
        values = dict(line.split("=", 1) for line in Path(environment["GITHUB_ENV"]).read_text().splitlines())
        self.assertEqual(set(values), {"GOCACHE", "GOMODCACHE", "GOTMPDIR", "TEMP", "TMP"})
        for value in values.values():
            directory = Path(value)
            self.assertTrue(directory.is_dir())
            self.assertIn(Path(environment["RUNNER_TEMP"]), directory.parents)
        self.assertEqual(values["TEMP"], values["TMP"])
        self.assertEqual(values["GOTMPDIR"], values["TMP"])

    def test_scratch_reconfiguration_rejects_non_hosted_and_other_platforms(self):
        environment = {**self.environment, "RUNNER_OS": "Windows", "GITHUB_ACTIONS": "true",
                       "RUNNER_ENVIRONMENT": "github-hosted"}
        for overrides in ({"RUNNER_OS": "macOS"}, {"GITHUB_ACTIONS": "false"},
                          {"RUNNER_ENVIRONMENT": "self-hosted"}):
            with self.subTest(overrides=overrides), self.assertRaises(ValueError):
                ci.windows_environment({**environment, **overrides})
        self.assertFalse(Path(self.environment["GITHUB_ENV"]).exists())

    def test_windows_installer_keeps_sdk_initialization_and_platform_cache_contracts(self):
        action = (Path(__file__).resolve().parent.parent / '.github/actions/setup-flutter/action.yml').read_text()
        self.assertIn('python tool/ci_fvm.py', action)
        self.assertIn("runner.os == 'Windows' && runner.arch == 'X64'", action)
        self.assertIn("runner.os == 'Windows' && runner.arch != 'X64'", action)
        self.assertIn("steps.paths.outputs.fvm_version", action)
        self.assertIn('fvm install\n', action)
        self.assertIn('fvm flutter --version', action)
        self.assertEqual(action.count('fvm flutter pub get'), 2)
        self.assertIn('fvm flutter precache "${artifact_flags[@]}"', action)
        self.assertIn("fail-on-cache-miss: ${{ inputs.require-cache-hit }}", action)

    def seed_environment(self):
        return {**self.environment, "GITHUB_ACTIONS": "true", "RUNNER_ENVIRONMENT": "github-hosted",
                "FVM_CACHE_PATH": str(Path(self.environment["RUNNER_TOOL_CACHE"]) / "bridra-fvm")}

    def seed_clone(self, command, **options):
        self.assertEqual(command[:-1], ['git', 'clone', '--depth=1', '--single-branch', '--branch',
                                       '3.44.6', 'https://github.com/flutter/flutter.git'])
        self.assertTrue(options['check'])
        self.assertEqual(options['timeout'], 180)
        executable = Path(command[-1]) / 'bin/flutter'
        executable.parent.mkdir(parents=True)
        executable.write_text('#!/bin/sh\nexit 0\n')
        executable.chmod(0o755)

    def test_cold_sdk_seed_uses_exact_official_tag_before_fvm_initialization(self):
        environment = self.seed_environment()
        with patch('ci.subprocess.run', side_effect=self.seed_clone), \
                patch('ci.subprocess.check_output', return_value='3.44.6\n') as tag:
            ci.seed_flutter(self.root, environment)
        destination = Path(environment['FVM_CACHE_PATH']) / 'versions/3.44.6'
        self.assertTrue((destination / 'bin/flutter').is_file())
        self.assertEqual(tag.call_args.args[0][-4:], ['describe', '--tags', '--exact-match', 'HEAD'])
        self.assertEqual(list(destination.parent.iterdir()), [destination])
        action = (Path(__file__).resolve().parent.parent / '.github/actions/setup-flutter/action.yml').read_text()
        self.assertIn('if [[ "$RUNNER_OS" != "Windows" ]]; then python3 tool/ci.py seed-flutter; fi', action)
        self.assertLess(action.index('ci.py seed-flutter'), action.index('fvm install\n'))

    def test_seed_retains_existing_sdk_including_invalid_entry_for_fvm_to_reject(self):
        environment = self.seed_environment()
        destination = Path(environment['FVM_CACHE_PATH']) / 'versions/3.44.6'
        destination.mkdir(parents=True)
        marker = destination / 'existing'
        marker.write_text('preserve')
        with patch('ci.subprocess.run') as clone:
            ci.seed_flutter(self.root, environment)
        clone.assert_not_called()
        self.assertEqual(marker.read_text(), 'preserve')

    def test_seed_rejects_local_self_hosted_windows_and_unexpected_cache_before_mutation(self):
        environment = self.seed_environment()
        for overrides in ({'GITHUB_ACTIONS': 'false'}, {'RUNNER_ENVIRONMENT': 'self-hosted'},
                          {'RUNNER_OS': 'Windows'}, {'FVM_CACHE_PATH': str(self.root / 'other')},
                          {'FVM_CACHE_PATH': 'relative'}):
            with self.subTest(overrides=overrides), patch('ci.subprocess.run') as clone, \
                    self.assertRaises(ValueError):
                ci.seed_flutter(self.root, {**environment, **overrides})
            clone.assert_not_called()
        self.assertFalse(Path(environment['FVM_CACHE_PATH']).exists())

    def test_wrong_tag_nonexecutable_and_failed_clone_never_publish_partial_sdk(self):
        environment = self.seed_environment()
        destination = Path(environment['FVM_CACHE_PATH']) / 'versions/3.44.6'
        def nonexecutable(command, **options):
            self.seed_clone(command, **options)
            (Path(command[-1]) / 'bin/flutter').chmod(0o644)
        for clone, tag in ((self.seed_clone, '3.44.5'), (nonexecutable, '3.44.6'),
                           (subprocess.CalledProcessError(1, 'git'), '3.44.6'),
                           (subprocess.TimeoutExpired('git', 180), '3.44.6')):
            with self.subTest(clone=clone, tag=tag), \
                    patch('ci.subprocess.run', side_effect=clone), \
                    patch('ci.subprocess.check_output', return_value=tag), \
                    self.assertRaises((ValueError, subprocess.CalledProcessError, subprocess.TimeoutExpired)):
                ci.seed_flutter(self.root, environment)
            self.assertFalse(destination.exists())
            self.assertEqual(list(destination.parent.iterdir()), [])

    def android_fixture(self, source='val ndkVersion: String = "28.2.13676358"'):
        sdk = self.root / 'android SDK'
        sdk.mkdir(exist_ok=True)
        manager = sdk / 'cmdline-tools/latest/bin/sdkmanager'
        manager.parent.mkdir(parents=True, exist_ok=True)
        manager.write_text('#!/bin/sh\nexit 0\n')
        manager.chmod(0o755)
        extension = self.root / 'flutter/packages/flutter_tools/gradle/src/main/kotlin/FlutterExtension.kt'
        extension.parent.mkdir(parents=True, exist_ok=True)
        extension.write_text(source)
        return {'ANDROID_HOME': str(sdk), 'FLUTTER_ROOT': str(self.root / 'flutter'),
                'GITHUB_OUTPUT': self.environment['GITHUB_OUTPUT']}

    def test_native_android_cache_only_selects_exact_sdk_tool_versions(self):
        environment = self.android_fixture()
        ci.android_native_environment(environment)
        values = dict(line.split('=', 1) for line in Path(environment['GITHUB_OUTPUT']).read_text().splitlines())
        self.assertEqual(values['ndk'], '28.2.13676358')
        self.assertEqual(values['cmake'], ci.ANDROID_CMAKE_VERSION)
        self.assertEqual(values['ndk_path'], environment['ANDROID_HOME'] + '/ndk/28.2.13676358')
        self.assertEqual(values['cmake_path'], environment['ANDROID_HOME'] + '/cmake/' + ci.ANDROID_CMAKE_VERSION)
        self.assertEqual(values['sdkmanager_path'], environment['ANDROID_HOME'] + '/cmdline-tools/latest/bin/sdkmanager')
        self.assertEqual(set(values), {'ndk', 'cmake', 'ndk_path', 'cmake_path', 'sdkmanager_path'})

    def test_missing_or_injected_ndk_and_missing_android_sdk_fail_before_export(self):
        for source in ('', 'val ndkVersion: String = "latest"', 'val ndkVersion: String = "28.2.1\\nother=value"'):
            environment = self.android_fixture(source)
            with self.subTest(source=source), self.assertRaises(ValueError):
                ci.android_native_environment(environment)
            self.assertFalse(Path(environment['GITHUB_OUTPUT']).exists())
        environment = self.android_fixture()
        environment['ANDROID_HOME'] += '/missing'
        with self.assertRaises(ValueError):
            ci.android_native_environment(environment)

    def test_android_native_cache_writer_installs_both_tools_and_keeps_full_builds(self):
        action = (Path(__file__).resolve().parent.parent / '.github/actions/setup-flutter/action.yml').read_text()
        self.assertIn('android-native-v1-${{ runner.os }}-${{ runner.arch }}', action)
        self.assertIn('BRIDRA_ANDROID_SDKMANAGER: ${{ steps.android-native.outputs.sdkmanager_path }}', action)
        self.assertIn('"$BRIDRA_ANDROID_SDKMANAGER" --install', action)
        self.assertIn("'ndk;${{ steps.android-native.outputs.ndk }}' 'cmake;${{ steps.android-native.outputs.cmake }}'", action)
        self.assertIn('${{ steps.android-native.outputs.ndk_path }}', action)
        self.assertIn('${{ steps.android-native.outputs.cmake_path }}', action)
        self.assertNotIn('/ndk/*', action)

    def test_sdkmanager_absolute_path_runs_with_spaces_and_no_path_entry(self):
        environment = self.android_fixture()
        manager = Path(environment['ANDROID_HOME']) / 'cmdline-tools/latest/bin/sdkmanager'
        manager.write_text(f'#!{sys.executable}\nimport json,os,sys\nopen(os.environ["CALLS"],"w").write(json.dumps(sys.argv[1:]))\n')
        ci.android_native_environment(environment)
        values = dict(line.split('=', 1) for line in Path(environment['GITHUB_OUTPUT']).read_text().splitlines())
        action = (Path(__file__).resolve().parent.parent / '.github/actions/setup-flutter/action.yml').read_text()
        command = action.split('    - name: Complete the Android native tool cache profile', 1)[1].split('      run: |\n', 1)[1]
        command = command.replace('${{ steps.android-native.outputs.ndk }}', values['ndk'])
        command = command.replace('${{ steps.android-native.outputs.cmake }}', values['cmake'])
        calls = self.root / 'calls.json'
        process = subprocess.run(['/bin/bash', '-e', '-c', command], capture_output=True, text=True,
                                 env={**os.environ, 'PATH': '/missing', 'CALLS': str(calls),
                                      'BRIDRA_ANDROID_SDKMANAGER': values['sdkmanager_path']}, timeout=5)
        self.assertEqual(process.returncode, 0, process.stdout + process.stderr)
        self.assertEqual(json.loads(calls.read_text()), ['--install', 'ndk;' + values['ndk'], 'cmake;' + values['cmake']])

    def test_missing_sdkmanager_blocks_profile_export_instead_of_relying_on_path(self):
        environment = self.android_fixture()
        (Path(environment['ANDROID_HOME']) / 'cmdline-tools/latest/bin/sdkmanager').unlink()
        with self.assertRaises(ValueError):
            ci.android_native_environment(environment)
        self.assertFalse(Path(environment['GITHUB_OUTPUT']).exists())

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


class SimulatorPrebootExperimentTests(unittest.TestCase):
    def setUp(self):
        root = Path(__file__).resolve().parent.parent
        self.workflow = (root / '.github/workflows/ios-simulator-preboot-experiment.yml').read_text()

    def test_trial_is_opt_in_same_repository_and_bounded_to_four_samples(self):
        self.assertIn("github.event.label.name == 'benchmark-ios-preboot'", self.workflow)
        self.assertIn('github.event.pull_request.head.repo.full_name == github.repository', self.workflow)
        self.assertIn("github.event.pull_request.base.ref == 'main'", self.workflow)
        self.assertNotIn('pull_request_target:', self.workflow)
        self.assertNotIn('synchronize', self.workflow)
        self.assertIn('contents: read', self.workflow)
        self.assertIn('persist-credentials: false', self.workflow)
        self.assertIn('runs-on: macos-15', self.workflow)
        self.assertIn('fail-fast: false', self.workflow)
        self.assertIn('max-parallel: 2', self.workflow)
        samples = re.findall(r'- preparation: (early|deferred)\n            sample: ([12])', self.workflow)
        self.assertEqual(samples, [('early', '1'), ('deferred', '1'), ('deferred', '2'), ('early', '2')])

    def test_only_preparation_position_changes_and_warm_caches_are_required(self):
        early = self.workflow.index('name: Start Simulator before toolchain setup (A)')
        go = self.workflow.index('name: Set up Go')
        flutter = self.workflow.index('name: Set up pinned Flutter')
        deferred = self.workflow.index('name: Start Simulator after toolchain setup (B)')
        smoke = self.workflow.index('name: Exercise iOS Simulator HTTP round trip')
        self.assertLess(early, go)
        self.assertLess(go, flutter)
        self.assertLess(flutter, deferred)
        self.assertLess(deferred, smoke)
        self.assertEqual(self.workflow.count('ios_simulator_prepare.py start --state'), 2)
        self.assertIn("if: matrix.preparation == 'early'", self.workflow)
        self.assertIn("if: matrix.preparation == 'deferred'", self.workflow)
        self.assertIn('GO_CACHE_HIT: ${{ steps.go.outputs.cache-hit }}', self.workflow)
        self.assertIn('run: test "$GO_CACHE_HIT" = true', self.workflow)
        self.assertIn("require-cache-hit: 'true'", self.workflow)
        self.assertIn('backend/integration/sqljobstore/go.mod', self.workflow)
        self.assertNotIn('cache-namespace:', self.workflow)
        self.assertNotIn('actions/download-artifact', self.workflow)

    def test_both_samples_keep_the_original_smoke_retry_and_owned_cleanup(self):
        self.assertEqual(self.workflow.count('bash tool/ios_ci_smoke.sh "$bridra_simulator_device"'), 2)
        self.assertEqual(self.workflow.count('ios_simulator_prepare.py device --state'), 2)
        self.assertEqual(self.workflow.count('ios_simulator_prepare.py ready --state'), 2)
        self.assertNotIn('outputs.device', self.workflow)
        self.assertEqual(self.workflow.count('timeout-minutes: 12'), 2)
        self.assertEqual(self.workflow.count("if: steps.ios_simulator_smoke.outcome == 'failure'"), 2)
        self.assertEqual(self.workflow.count('continue-on-error: true'), 1)
        initial = self.workflow.split('id: ios_simulator_smoke', 1)[1].split('- name: Reset', 1)[0]
        self.assertIn('ios_simulator_prepare.py ready --state', initial)
        self.assertIn("TIMEFORMAT='Simulator readiness seconds: %3R'", initial)
        self.assertIn("TIMEFORMAT='Simulator smoke seconds: %3R'", initial)
        self.assertIn('time python3 tool/ios_simulator_prepare.py ready --state', initial)
        self.assertIn('ios_simulator_prepare.py reset --state', self.workflow)
        self.assertIn('if: always()\n        run: python3 tool/ios_simulator_prepare.py finish', self.workflow)
        self.assertIn('if: always()\n        uses: actions/upload-artifact@v7', self.workflow)
        self.assertNotIn('erase all', self.workflow)
        self.assertNotIn('shutdown all', self.workflow)


class WorkflowWiringTests(unittest.TestCase):
    def job(self, name):
        workflow = (Path(__file__).resolve().parent.parent / '.github/workflows/checks.yml').read_text()
        return re.split(r'\n  [a-z][a-z0-9-]*:\n', workflow.split(f'\n  {name}:\n', 1)[1], 1)[0]

    def test_windows_detailed_logging_does_not_filter_native_suites(self):
        windows = self.job('windows-verify')
        self.assertIn('GOFLAGS: -v', windows)
        self.assertNotIn('-run ', windows)
        self.assertNotIn('-short', windows)
        source = (Path(__file__).resolve().parent / 'windows.ps1').read_text()
        self.assertEqual(source.count('@("test", "./...")'), 2)

    def test_windows_verify_owns_its_complete_compiler_cache_profile(self):
        verify = self.job('windows-verify')
        bundle = self.job('windows-bundle')
        self.assertIn('            tool/windows.ps1\n', verify)
        self.assertNotIn('            tool/windows.ps1\n', bundle)
        for dependency in ('backend/go.mod', 'backend/integration/sqljobstore/go.mod'):
            self.assertIn(dependency, verify)
            self.assertIn(dependency, bundle)

    def test_windows_scratch_is_exported_before_go_cache_restore(self):
        for name in ('windows-verify', 'windows-bundle'):
            body = self.job(name)
            self.assertLess(body.index('tool/ci.py windows-env'), body.index('uses: actions/setup-go@v7'))

    def test_windows_common_checks_delegate_only_to_required_core_work(self):
        windows = self.job('windows-verify')
        self.assertIn('BRIDRA_SHARED_CHECKS_IN_CORE: ${{ inputs.full || fromJSON(inputs.plan).core }}', windows)
        self.assertIn('-SharedChecksInCore:($env:BRIDRA_SHARED_CHECKS_IN_CORE -eq "true")', windows)
        scope = ci.plan(['lib/ui/window/window_controls.dart'])
        needs = {key: {'result': 'success' if required else 'skipped'}
                 for key, required in ci.job_requirements(scope, False).items()}
        ci.check_results(scope, False, needs)
        for result in ('skipped', 'failure', 'cancelled'):
            needs['core']['result'] = result
            with self.assertRaises(ValueError):
                ci.check_results(scope, False, needs)

    def test_reusable_gate_depends_on_every_verification_and_packaging_job(self):
        path = Path(__file__).resolve().parent.parent / '.github/workflows/checks.yml'
        workflow = path.read_text().split('\njobs:\n', 1)[1]
        jobs = set(re.findall(r'^  ([a-z][a-z0-9-]*):$', workflow, re.MULTILINE))
        needs = re.search(r'^    needs: \[([^]]+)\]$', self.job('check-results'), re.MULTILINE)
        self.assertIsNotNone(needs)
        self.assertEqual({name.strip() for name in needs[1].split(',')}, jobs - {'check-results'})
        self.assertEqual(jobs - {'check-results'}, set(VerificationResultsTests.jobs))

    def test_group_gates_run_after_failed_dependencies_and_validate_every_shard(self):
        for group, expected in ShardGateTests.groups.items():
            body = self.job(group)
            self.assertIn('always() && (inputs.full || fromJSON(inputs.plan).', body)
            needs = re.search(r'^    needs: \[([^]]+)\]$', body, re.MULTILINE)
            self.assertEqual({name.strip() for name in needs[1].split(',')}, set(expected))
            self.assertIn(f'ci.py group --group {group} --plan "$CI_PLAN" --full "$CI_FULL" --needs "$CI_NEEDS"', body)
            self.assertIn('CI_PLAN: ${{ inputs.plan }}', body)
            self.assertIn('CI_NEEDS: ${{ toJSON(needs) }}', body)

    def test_release_reuse_requires_all_shards_and_group_gates(self):
        import release_verification
        expected = {'changes', 'ci-gate', 'checks / check-results'} | {
            'checks / ' + job for job in VerificationResultsTests.jobs if job != 'release-package'}
        self.assertEqual(set(release_verification.REQUIRED_JOBS), expected)
        self.assertEqual(len(release_verification.REQUIRED_JOBS), len(expected))

    def test_ci_preserves_each_suite_and_collects_all_coverage_before_enforcing_floors(self):
        self.assertIn('run: make verify-checks', self.job('core-static'))
        self.assertIn('run: make backend-sql-store-test backend-coverage BACKEND_COVERAGE_FLAGS=-race', self.job('core-go'))
        self.assertIn('run: make flutter-package-coverage flutter-app-coverage flutter-web-test', self.job('core-flutter'))
        gate = self.job('core')
        self.assertIn('make coverage-report', gate)
        self.assertNotIn('make verify-coverage', gate)
        self.assertNotIn('make coverage-check', gate)
        self.assertIn('name: core-go-coverage-${{ github.run_attempt }}\n          path: coverage', gate)
        self.assertIn('name: core-flutter-coverage-${{ github.run_attempt }}\n          path: .', gate)
        for profile in ('coverage/go.out', 'coverage/lcov.info', 'packages/bridra_flutter/coverage/lcov.info'):
            self.assertIn(profile, self.job('core-go') + self.job('core-flutter'))
            self.assertIn(profile, gate)
        self.assertLess(gate.index('Collect both required Flutter profiles'), gate.index('make coverage-report'))

    def test_simulator_preparation_overlaps_setup_with_one_retry_and_always_cleanup(self):
        body = self.job('ios-simulator')
        self.assertLess(body.index('name: Set up Go'), body.index('ios_ci_prepare.py begin'))
        self.assertLess(body.index('ios_ci_prepare.py begin'), body.index('name: Set up pinned Flutter'))
        self.assertIn('ios_ci_prepare.py ready', body)
        self.assertIn('ios_simulator_prepare.py ready', body)
        self.assertIn('ios_ci_prepare.py reset', body)
        self.assertEqual(body.count('bash tool/ios_ci_smoke.sh "$bridra_simulator_device"'), 2)
        self.assertEqual(body.count('ios_simulator_prepare.py device --state'), 2)
        self.assertEqual(body.count('ios_simulator_prepare.py ready --state'), 1)
        self.assertNotIn('outputs.device', body)
        self.assertEqual(body.count('timeout-minutes: 12'), 2)
        self.assertIn('if: always()\n        run: python3 tool/ios_ci_prepare.py finish', body)
        self.assertIn('BRIDRA_IOS_SIMULATOR_DIAGNOSTICS_DIR:', body)
        self.assertNotIn('erase all', body)
        self.assertNotIn('shutdown all', body)
        self.assertIn('make ios-simulator-watchdog-test', self.job('ios-watchdog'))
        self.assertIn('ios_ci_smoke_test.py', self.job('ios-watchdog'))
        self.assertIn('ios_ci_prepare_test.py', self.job('ios-watchdog'))
        self.assertNotIn('ios_ci_smoke_test.py', self.job('core-static'))
        self.assertIn('make ios-build BACKEND_URL=', self.job('ios-build'))

    def test_simulator_uses_standard_intel_with_existing_arm_release_and_desktop_coverage(self):
        body = self.job('ios-simulator')
        self.assertIn('runs-on: macos-15-intel', body)
        self.assertIn('DEVELOPER_DIR: /Applications/Xcode_16.4.app/Contents/Developer', body)
        self.assertIn('sysctl hw.memsize hw.ncpu vm.swapusage', body)
        for name in ('ios-build', 'macos-lifecycle', 'macos-bundle'):
            self.assertIn('runs-on: macos-15\n', self.job(name))

    def test_macos_native_tests_and_bundle_use_independent_runners_and_strict_gate(self):
        lifecycle = self.job('macos-lifecycle')
        bundle = self.job('macos-bundle')
        gate = self.job('macos')
        self.assertIn('make macos-native-test', lifecycle)
        self.assertIn('make macos-sandbox-smoke', lifecycle)
        self.assertIn('make macos-smoke', bundle)
        self.assertNotIn('make macos-smoke', lifecycle)
        self.assertNotIn('macos-native-test', bundle)
        self.assertIn('needs: [macos-lifecycle, macos-bundle]', gate)
        self.assertIn('group --group macos', gate)
        self.assertIn('macos/RunnerTests/RunnerTests.swift', lifecycle)
        self.assertIn('macos/Runner.xcodeproj/project.pbxproj', bundle)
        root = Path(__file__).resolve().parent.parent
        commands = subprocess.check_output(['make', '-n', 'macos-native-test', 'macos-sandbox-smoke'],
                                           cwd=root, text=True)
        self.assertEqual(commands.count('xcodebuild -version'), 1)
        self.assertEqual(commands.count('xcodebuild test'), 1)
        self.assertIn('-parallel-testing-enabled NO', commands)
        self.assertIn('BRIDRA_MACOS_SANDBOX_SMOKE=1', commands)
        self.assertIn('-count=1', commands)

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
