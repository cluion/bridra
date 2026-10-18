import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


class IOSCIStagesTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / 'tool').mkdir()
        shutil.copyfile(Path(__file__).with_name('ios_ci_smoke.sh'), self.root / 'tool/ios_ci_smoke.sh')
        binary = self.root / 'bin'
        binary.mkdir()
        (binary / 'dirname').symlink_to(shutil.which('dirname'))
        self.write_executable(binary / 'make', '''#!/bin/bash
echo "$*" >> "$CALLS"
if [[ "$*" == macos-check ]]; then exit "${XCODE_STATUS:-0}"; fi
if [[ "$*" == backend-server-build ]]; then exit "${BUILD_STATUS:-0}"; fi
exit 99
''')
        self.write_executable(self.root / 'tool/ios_simulator_smoke.sh', '''#!/bin/bash
echo smoke >> "$CALLS"
[[ "$BRIDRA_IOS_SIMULATOR_DEVICE" == "$EXPECTED_DEVICE" ]] || exit 90
[[ "$BRIDRA_SERVER_PATH" == "$EXPECTED_ROOT/build/server/bridra_server" ]] || exit 91
[[ "$BRIDRA_FLUTTER" == "fvm flutter --verbose" ]] || exit 92
[[ "$BRIDRA_IOS_SIMULATOR_PORT" == "$EXPECTED_PORT" ]] || exit 93
[[ "$BRIDRA_IOS_SIMULATOR_DIAGNOSTICS_DIR" == "$EXPECTED_ROOT/diagnostics" ]] || exit 94
exit "${SMOKE_STATUS:-0}"
''')
        self.device = '01234567-89AB-CDEF-0123-456789ABCDEF'
        self.environment = dict(os.environ, PATH=str(binary), GITHUB_ACTIONS='true',
                                RUNNER_ENVIRONMENT='github-hosted', EXPECTED_DEVICE=self.device,
                                EXPECTED_ROOT=str(self.root), EXPECTED_PORT='18080',
                                CALLS=str(self.root / 'calls'),
                                BRIDRA_IOS_SIMULATOR_DIAGNOSTICS_DIR=str(self.root / 'diagnostics'))
        self.environment.pop('BRIDRA_IOS_SIMULATOR_PORT', None)

    def write_executable(self, path, source):
        path.write_text(source)
        path.chmod(0o755)

    def run_script(self, *devices):
        return subprocess.run(['/bin/bash', str(self.root / 'tool/ios_ci_smoke.sh'), *devices],
                              env=self.environment, capture_output=True, text=True, timeout=5)

    def calls(self):
        path = self.root / 'calls'
        return path.read_text().splitlines() if path.exists() else []

    def test_same_prerequisites_and_native_smoke_run_once_in_order(self):
        result = self.run_script(self.device)
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        self.assertEqual(self.calls(), ['macos-check', 'backend-server-build', 'smoke'])
        for name in ('xcode-check', 'backend-build', 'transport-smoke'):
            self.assertRegex(result.stderr, rf'Bridra iOS {name} seconds: [0-9]+\.[0-9]{{3}}')
            self.assertIn(f'Bridra iOS {name} exit: 0', result.stdout)

    def test_preflight_build_and_smoke_failures_propagate_and_stop(self):
        for key, expected in [('XCODE_STATUS', ['macos-check']),
                              ('BUILD_STATUS', ['macos-check', 'backend-server-build']),
                              ('SMOKE_STATUS', ['macos-check', 'backend-server-build', 'smoke'])]:
            with self.subTest(stage=key):
                self.environment.pop('XCODE_STATUS', None)
                self.environment.pop('BUILD_STATUS', None)
                self.environment[key] = '65'
                path = self.root / 'calls'
                path.unlink(missing_ok=True)
                result = self.run_script(self.device)
                self.assertEqual(result.returncode, 65, result.stderr + result.stdout)
                self.assertEqual(self.calls(), expected)
                self.assertIn('exit: 65', result.stdout)

    def test_non_hosted_or_local_execution_fails_before_native_commands(self):
        for key, value in [('GITHUB_ACTIONS', 'false'), ('RUNNER_ENVIRONMENT', 'self-hosted')]:
            with self.subTest(key=key):
                previous = self.environment[key]
                self.environment[key] = value
                result = self.run_script(self.device)
                self.environment[key] = previous
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(self.calls(), [])

    def test_missing_invalid_or_extra_devices_fail_before_native_commands(self):
        for devices in [(), ('invalid',), (self.device, self.device), ('x\nother',)]:
            with self.subTest(devices=devices):
                self.assertNotEqual(self.run_script(*devices).returncode, 0)
                self.assertEqual(self.calls(), [])

    def test_custom_port_and_diagnostics_are_preserved(self):
        self.environment['BRIDRA_IOS_SIMULATOR_PORT'] = self.environment['EXPECTED_PORT'] = '19080'
        result = self.run_script(self.device)
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)


if __name__ == '__main__':
    unittest.main()
