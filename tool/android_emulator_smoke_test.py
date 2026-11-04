import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
import unittest


ROOT = Path(__file__).resolve().parents[1]
SMOKE = ROOT / 'tool/android_emulator_smoke.sh'
TOKEN = 'android-emulator-smoke-token'
BEARER = 'private-fixture-value-27x'
PNG = b'\x89PNG\r\n\x1a\nfixture-screen'

FAKE_TOOL = r'''
import json
import os
from pathlib import Path
import signal
import sys
import time

root = Path(os.environ['SMOKE_FIXTURE'])
name = Path(sys.argv[0]).name
args = sys.argv[1:]
mode = os.environ.get('SMOKE_MODE', 'fail')
with (root / 'processes.jsonl').open('a') as output:
    output.write(json.dumps({'name': name, 'pid': os.getpid()}) + '\n')

if name == 'adb':
    with (root / 'adb.jsonl').open('a') as output:
        output.write(json.dumps(args) + '\n')
    command = args[2:] if args[:1] == ['-s'] else args
    if command[:1] == ['devices']:
        print('List of devices attached\nemulator-5554\tdevice')
    elif command == ['shell', 'getprop', 'sys.boot_completed']:
        print('1')
    elif command == ['wait-for-device']:
        pass
    else:
        if os.environ.get('ADB_UNAVAILABLE'):
            print('adb: device offline')
            sys.exit(1)
        if command == ['shell', 'dumpsys', 'activity', 'activities']:
            if os.environ.get('HANG_DIAGNOSTIC'):
                signal.signal(signal.SIGTERM, signal.SIG_IGN)
                time.sleep(60)
            flutter = root / 'flutter.pid'
            if flutter.exists():
                try:
                    os.kill(int(flutter.read_text()), 0)
                    print('Flutter process alive during capture')
                except ProcessLookupError:
                    pass
        if command == ['exec-out', 'screencap', '-p']:
            sys.stdout.buffer.write(b'\x89PNG\r\n\x1a\nfixture-screen')
        else:
            if command[:1] == ['logcat'] and os.environ.get('LARGE_LOG'):
                print('x' * (2 * 1024 * 1024 + 100))
            print('BRIDRA_BACKEND_TOKEN=android-emulator-smoke-token')
            print('Authorization: Bearer private-fixture-value-27x')
elif name == 'backend':
    counter = root / 'backend-starts'
    count = int(counter.read_text()) + 1 if counter.exists() else 1
    pending = root / 'backend-starts.pending'
    pending.write_text(str(count))
    pending.replace(counter)
    print('server: listening on http://127.0.0.1:18082/rpc', flush=True)
    if mode in ('success', 'late-fail'):
        for method in ('system.health', 'greeting.hello', 'bridra.smoke.stream',
                       'bridra.smoke.upload.verify'):
            print(json.dumps({'rpc_method': method}, separators=(',', ':')), flush=True)
        print('{"http_method":"GET","surface":"file_transfer"}', flush=True)
        print('server: smoke download resumed at offset 32768', flush=True)
        print('server: smoke upload resumed at offset 32768', flush=True)
    while True:
        time.sleep(0.01)
elif name == 'flutter':
    (root / 'flutter.pid').write_text(str(os.getpid()))
    (root / 'flutter-args.json').write_text(json.dumps(args))
    for line in ("Running Gradle task 'assembleDebug'...",
                 'Built build/app/outputs/flutter-apk/app-debug.apk',
                 'Installing build/app/outputs/flutter-apk/app-debug.apk...',
                 'executing: adb -s emulator-5554 shell am start -n example/MainActivity',
                 'Waiting for VM Service port to be available...',
                 'test 0: Connecting to vm service',
                 'Finding the correct isolate with the integration test service extension',
                 'BRIDRA_BACKEND_TOKEN=android-emulator-smoke-token',
                 'Authorization: Bearer private-fixture-value-27x'):
        print(line, flush=True)
    if mode in ('success', 'late-fail'):
        while int((root / 'backend-starts').read_text()) < 2:
            time.sleep(0.01)
        time.sleep(0.1)
        sys.exit(0 if mode == 'success' else 23)
    if mode == 'hang':
        while True:
            time.sleep(0.01)
    sys.exit(17)
elif name == 'sleep':
    time.sleep(0.01)
elif name == 'date':
    if args == ['+%s']:
        counter = root / 'clock'
        stamp = int(counter.read_text()) + 5 if counter.exists() else 1700000000
        counter.write_text(str(stamp))
        print(stamp)
    else:
        print('2026-11-04T00:00:00Z')
else:
    sys.exit(99)
'''


class AndroidSmokeDiagnosticsTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='bridra-android-smoke-test-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.bin = self.root / 'bin'
        self.bin.mkdir()
        self.scratch = self.root / 'scratch'
        self.scratch.mkdir()
        self.diagnostics = self.root / 'diagnostics with spaces'
        for name in ('adb', 'backend', 'flutter', 'sleep'):
            path = self.bin / name
            path.write_text(f'#!{sys.executable}\n' + FAKE_TOOL)
            path.chmod(0o755)
        self.environment = dict(
            os.environ, PATH=str(self.bin) + os.pathsep + os.environ['PATH'],
            TMPDIR=str(self.scratch), SMOKE_FIXTURE=str(self.root),
            BRIDRA_SERVER_PATH=str(self.bin / 'backend'), BRIDRA_FLUTTER='flutter',
            BRIDRA_ANDROID_EMULATOR_DEVICE='emulator-5554',
            BRIDRA_ANDROID_EMULATOR_PORT='18082',
            BRIDRA_ANDROID_EMULATOR_DIAGNOSTICS_DIR=str(self.diagnostics),
        )
        for name in ('HANG_DIAGNOSTIC', 'ADB_UNAVAILABLE', 'LARGE_LOG'):
            self.environment.pop(name, None)

    def run_smoke(self, mode='fail', terminate=False, **environment):
        env = dict(self.environment, SMOKE_MODE=mode, **environment)
        process = subprocess.Popen(
            ['/bin/sh', str(SMOKE)], env=env, cwd=self.root,
            stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
            text=True, start_new_session=True,
        )
        try:
            if terminate:
                deadline = time.monotonic() + 5
                while not (self.root / 'flutter.pid').exists():
                    if process.poll() is not None or time.monotonic() >= deadline:
                        self.fail('Fixture did not reach Flutter startup')
                    time.sleep(0.01)
                process.send_signal(signal.SIGTERM)
            output, _ = process.communicate(timeout=20)
            self.assert_no_process_or_scratch_leaks()
            return subprocess.CompletedProcess(process.args, process.returncode, output)
        finally:
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            process.wait()

    def assert_no_process_or_scratch_leaks(self):
        self.assertEqual(list(self.scratch.iterdir()), [])
        for row in (self.root / 'processes.jsonl').read_text().splitlines():
            process = json.loads(row)
            with self.assertRaises(ProcessLookupError, msg=process):
                os.kill(process['pid'], 0)

    def assert_redacted(self, result):
        texts = [result.stdout]
        texts += [p.read_text() for p in self.diagnostics.iterdir() if p.suffix != '.png']
        for text in texts:
            self.assertNotIn(TOKEN, text)
            self.assertNotIn(BEARER, text)
        self.assertIn('[REDACTED]', (self.diagnostics / 'flutter-test.log').read_text())

    def test_success_keeps_full_reconnect_checks_without_failure_artifact(self):
        result = self.run_smoke('success')
        self.assertEqual(result.returncode, 0, result.stdout)
        self.assertFalse(self.diagnostics.exists())
        self.assertEqual((self.root / 'backend-starts').read_text(), '2')
        arguments = json.loads((self.root / 'flutter-args.json').read_text())
        for argument in ('--verbose', '--dart-define=BRIDRA_SMOKE_STREAM=true',
                         '--dart-define=BRIDRA_SMOKE_DOWNLOAD=true',
                         '--dart-define=BRIDRA_SMOKE_UPLOAD_RESUME=true',
                         '--dart-define=BRIDRA_SMOKE_RECONNECT=true'):
            self.assertIn(argument, arguments)
        self.assertIn('stage=first_health_completed ', result.stdout)
        self.assertIn('stage=first_stream_completed ', result.stdout)
        self.assertNotIn(TOKEN, result.stdout)
        self.assertNotIn(BEARER, result.stdout)
        self.assert_no_process_or_scratch_leaks()

    def test_early_failure_preserves_status_logs_milestones_and_screen(self):
        result = self.run_smoke()
        self.assertEqual(result.returncode, 17, result.stdout)
        for name in ('summary.txt', 'flutter-test.log', 'backend.log', 'milestones.log',
                     'devices.txt', 'host-processes.txt', 'device-state.txt', 'logcat.txt',
                     'activities.txt', 'app-processes.txt', 'forwards.txt', 'commands.txt'):
            self.assertTrue((self.diagnostics / name).is_file(), name)
        self.assertEqual((self.diagnostics / 'screenshot.png').read_bytes(), PNG)
        self.assertIn('exit_status=17', (self.diagnostics / 'summary.txt').read_text())
        milestones = (self.diagnostics / 'milestones.log').read_text()
        for stage in ('script_started', 'emulator_ready', 'backend_ready', 'flutter_test_started',
                      'apk_build_started', 'apk_built', 'apk_install_observed',
                      'activity_launch_requested', 'vm_service_discovery_started',
                      'vm_service_discovered', 'test_isolate_discovery_started'):
            self.assertEqual(milestones.count(f'stage={stage} '), 1, milestones)
        self.assertNotIn('first_health_completed', milestones)
        calls = [json.loads(line) for line in (self.root / 'adb.jsonl').read_text().splitlines()]
        self.assertIn(['-s', 'emulator-5554', 'logcat', '-b', 'main', '-b', 'system',
                       '-b', 'crash', '-d', '-t', '2000', '-v', 'threadtime'], calls)
        self.assert_redacted(result)
        self.assert_no_process_or_scratch_leaks()

    def test_hung_adb_capture_is_bounded_without_hiding_original_failure(self):
        result = self.run_smoke(HANG_DIAGNOSTIC='1')
        self.assertEqual(result.returncode, 17, result.stdout)
        commands = (self.diagnostics / 'commands.txt').read_text()
        self.assertRegex(commands, r'activities exit_status=\d+ timed_out=true')
        self.assertIn('forwards exit_status=0 timed_out=false', commands)
        self.assertEqual((self.diagnostics / 'screenshot.png').read_bytes(), PNG)
        self.assert_redacted(result)
        self.assert_no_process_or_scratch_leaks()

    def test_offline_device_retains_partial_diagnostics_and_failure(self):
        result = self.run_smoke(ADB_UNAVAILABLE='1')
        self.assertEqual(result.returncode, 17, result.stdout)
        self.assertIn('adb: device offline', (self.diagnostics / 'logcat.txt').read_text())
        self.assertFalse((self.diagnostics / 'screenshot.png').exists())
        self.assertTrue((self.diagnostics / 'screenshot.txt').is_file())
        self.assertIn('screenshot exit_status=1', (self.diagnostics / 'commands.txt').read_text())
        self.assert_no_process_or_scratch_leaks()

    def test_collection_stops_at_total_budget_without_hiding_failure(self):
        path = self.bin / 'date'
        path.write_text(f'#!{sys.executable}\n' + FAKE_TOOL)
        path.chmod(0o755)
        result = self.run_smoke()
        self.assertEqual(result.returncode, 17, result.stdout)
        self.assertIn('skipped: diagnostic time budget exhausted',
                      (self.diagnostics / 'commands.txt').read_text())
        self.assert_no_process_or_scratch_leaks()

    def test_unwritable_artifact_destination_preserves_failure_and_cleanup(self):
        self.diagnostics.write_text('existing file')
        result = self.run_smoke()
        self.assertEqual(result.returncode, 17, result.stdout)
        self.assertIn('Unable to create Android smoke diagnostics directory.', result.stdout)
        self.assertEqual(self.diagnostics.read_text(), 'existing file')
        self.assert_no_process_or_scratch_leaks()

    def test_late_flutter_failure_still_collects_and_preserves_exit_status(self):
        result = self.run_smoke('late-fail')
        self.assertEqual(result.returncode, 23, result.stdout)
        self.assertIn('exit_status=23', (self.diagnostics / 'summary.txt').read_text())
        self.assertIn('stage=first_health_completed ', (self.diagnostics / 'milestones.log').read_text())
        self.assert_redacted(result)
        self.assert_no_process_or_scratch_leaks()

    def test_term_collects_before_stopping_flutter_and_preserves_signal_status(self):
        result = self.run_smoke('hang', terminate=True)
        self.assertEqual(result.returncode, 143, result.stdout)
        self.assertIn('reason=terminated', (self.diagnostics / 'summary.txt').read_text())
        self.assertIn('Flutter process alive during capture',
                      (self.diagnostics / 'activities.txt').read_text())
        self.assert_no_process_or_scratch_leaks()

    def test_diagnostics_are_opt_in_and_text_files_are_bounded(self):
        result = self.run_smoke(BRIDRA_ANDROID_EMULATOR_DIAGNOSTICS_DIR='')
        self.assertEqual(result.returncode, 17, result.stdout)
        self.assertFalse(self.diagnostics.exists())
        self.assert_no_process_or_scratch_leaks()
        result = self.run_smoke(LARGE_LOG='1')
        self.assertEqual(result.returncode, 17, result.stdout)
        self.assertEqual((self.diagnostics / 'logcat.txt').stat().st_size, 2 * 1024 * 1024)
        self.assert_redacted(result)
        self.assert_no_process_or_scratch_leaks()


if __name__ == '__main__':
    unittest.main()
