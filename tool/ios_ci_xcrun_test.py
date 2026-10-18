"""Fake-only scheduling tests; never invoke Apple's native tools."""

import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


class CompilerSchedulingTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.actual = self.root / 'native tools/xcrun'
        self.actual.parent.mkdir()
        self.actual.write_text(f'#!{sys.executable}\n' + '''import json, os, sys
from pathlib import Path
Path(os.environ['CALLS']).write_text(json.dumps(sys.argv[1:]))
sys.exit(int(os.environ.get('STATUS', '0')))
''')
        self.actual.chmod(0o755)
        self.calls = self.root / 'calls.json'
        self.environment = dict(os.environ, GITHUB_ACTIONS='true', RUNNER_ENVIRONMENT='github-hosted',
                                BRIDRA_IOS_CI_REAL_XCRUN=str(self.actual),
                                BRIDRA_IOS_CI_XCODE_JOBS='1', CALLS=str(self.calls))
        self.command = ['/bin/sh', str(Path(__file__).with_name('ios_ci_xcrun.sh'))]
        self.build = ['xcodebuild', '-configuration', 'Debug', '-sdk', 'iphonesimulator',
                      '-destination', 'id=fixture', '-resultBundlePath', str(self.root / 'result bundle'),
                      'DART_DEFINES=keep the exact argument']

    def run_wrapper(self, arguments):
        self.calls.unlink(missing_ok=True)
        return subprocess.run([*self.command, *arguments], env=self.environment,
                              capture_output=True, text=True, timeout=5)

    def test_actual_build_keeps_every_argument_and_injects_only_bounded_jobs(self):
        for jobs in ('1', '2', '3'):
            self.environment['BRIDRA_IOS_CI_XCODE_JOBS'] = jobs
            result = self.run_wrapper(self.build)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(json.loads(self.calls.read_text()), ['xcodebuild', '-jobs', jobs, *self.build[1:]])
            self.assertIn(f'Bridra iOS Xcode build concurrency: {jobs}', result.stderr)

    def test_queries_other_platform_builds_and_simulator_commands_are_unchanged(self):
        for arguments in (['xcodebuild', '-version'], ['xcodebuild', '-resolvePackageDependencies'],
                          ['xcodebuild', '-sdk', 'iphonesimulator', '-showBuildSettings'],
                          ['xcodebuild', '-sdk', 'iphoneos', '-resultBundlePath', 'bundle'],
                          ['simctl', 'launch', 'fixture', 'com.example.app'], ['simctl', 'bootstatus', 'fixture']):
            with self.subTest(arguments=arguments):
                result = self.run_wrapper(arguments)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(json.loads(self.calls.read_text()), arguments)
                self.assertNotIn('concurrency:', result.stderr)

    def test_real_command_failure_is_preserved(self):
        self.environment['STATUS'] = '73'
        result = self.run_wrapper(self.build)
        self.assertEqual(result.returncode, 73, result.stderr)

    def test_invalid_limits_and_non_hosted_execution_never_forward_a_command(self):
        for key, value in (('GITHUB_ACTIONS', 'false'), ('RUNNER_ENVIRONMENT', 'self-hosted'),
                           *(('BRIDRA_IOS_CI_XCODE_JOBS', value) for value in ('', '0', '4', '-1', '01', '1;exit 0')),
                           ('BRIDRA_IOS_CI_REAL_XCRUN', 'relative/xcrun')):
            previous = self.environment[key]
            self.environment[key] = value
            with self.subTest(key=key, value=value):
                result = self.run_wrapper(self.build)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(self.calls.exists())
            self.environment[key] = previous

    def test_conflicting_build_jobs_fail_before_native_commands(self):
        result = self.run_wrapper([*self.build, '-jobs', '3'])
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.calls.exists())


if __name__ == '__main__':
    unittest.main()
