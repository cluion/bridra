"""Mock-only contracts; these tests never operate on a local Simulator."""

import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import Mock, call, patch

import ios_simulator_prepare as prepare


class PreparationTests(unittest.TestCase):
    device = "11111111-2222-3333-4444-555555555555"
    other = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.path = self.root / "preparation/state.json"
        self.environment = {"GITHUB_ACTIONS": "true", "RUNNER_ENVIRONMENT": "github-hosted",
                            "RUNNER_TEMP": str(self.root), "GITHUB_OUTPUT": str(self.root / "output")}
        self.native = patch.object(prepare, "command")
        self.command = self.native.start()
        self.addCleanup(self.native.stop)
        self.command.return_value = Mock(stdout="", returncode=0)

    def state(self, owned=True, **extra):
        self.path.parent.mkdir(parents=True, exist_ok=True)
        prepare.save(self.path, {"schema": 1, "device": self.device, "owned": owned, **extra})

    def devices(self, *devices):
        return json.dumps({"devices": {"iOS-runtime": list(devices)}})

    def phone(self, state="Shutdown", device=None, **extra):
        return {"udid": device or self.device, "name": "iPhone 17", "state": state,
                "isAvailable": True, **extra}

    def test_only_ephemeral_hosted_runners_accept_state_inside_runner_temp(self):
        self.assertEqual(prepare.state_path(self.path, self.environment), self.path)
        for overrides in ({"GITHUB_ACTIONS": "false"}, {"RUNNER_ENVIRONMENT": "self-hosted"},
                          {"RUNNER_ENVIRONMENT": ""}):
            with self.subTest(overrides=overrides), self.assertRaises(ValueError):
                prepare.state_path(self.path, {**self.environment, **overrides})
        for path in (self.root, self.root / "../outside/state.json"):
            with self.subTest(path=path), self.assertRaises(ValueError):
                prepare.state_path(path, self.environment)
        self.command.assert_not_called()

    def test_every_cli_command_rejects_local_use_before_native_operations(self):
        for operation in ("start", "ready", "reset", "finish", "_boot"):
            with self.subTest(operation=operation), patch.dict(os.environ, {}, clear=True), \
                    patch.object(sys, "argv", ["prepare", operation, "--state", str(self.path)]):
                with self.assertRaises(ValueError):
                    prepare.main()
        self.command.assert_not_called()

    def test_symlink_cannot_escape_runner_temp(self):
        (self.root / "escape").symlink_to(self.root.parent, target_is_directory=True)
        with self.assertRaises(ValueError):
            prepare.state_path(self.root / "escape/state.json", self.environment)

    def test_prefer_existing_booted_device_without_creating_worker(self):
        self.command.return_value.stdout = self.devices(self.phone(), self.phone("Booted", self.other))
        with patch.object(prepare.subprocess, "Popen") as popen:
            self.assertEqual(prepare.start(self.path), self.other)
        popen.assert_not_called()
        self.assertFalse(prepare.load(self.path)["owned"])
        self.command.assert_called_once_with("list", "devices", "available", "--json", timeout=120)

    def test_start_owned_existing_phone_in_bounded_detached_worker(self):
        self.command.return_value.stdout = self.devices(self.phone(name="iPad"), self.phone())
        with patch.object(prepare.subprocess, "Popen", return_value=Mock(pid=123)) as popen:
            self.assertEqual(prepare.start(self.path), self.device)
        args, kwargs = popen.call_args
        self.assertEqual(args[0], [sys.executable, str(Path(prepare.__file__).resolve()),
                                  "_boot", "--state", str(self.path)])
        self.assertEqual(kwargs["stdin"], subprocess.DEVNULL)
        self.assertEqual(kwargs["stderr"], subprocess.STDOUT)
        self.assertTrue(kwargs["start_new_session"])
        self.assertEqual(kwargs["stdout"].name, str(self.path.with_suffix(".log")))
        self.assertEqual(prepare.load(self.path)["worker_pid"], 123)
        self.command.assert_called_once_with("list", "devices", "available", "--json", timeout=120)

    def test_cold_discovery_timeout_keeps_ownership_empty_and_starts_no_worker(self):
        self.command.side_effect = subprocess.TimeoutExpired("simctl list", 120)
        with patch.object(prepare.subprocess, "Popen") as popen, self.assertRaises(subprocess.TimeoutExpired):
            prepare.start(self.path)
        self.command.assert_called_once_with("list", "devices", "available", "--json", timeout=120)
        self.assertFalse(self.path.exists())
        popen.assert_not_called()

    def test_missing_or_unavailable_phone_does_not_spawn_or_create_a_device(self):
        for devices in ([], [self.phone(isAvailable=False)], [self.phone(name="iPad")]):
            self.command.return_value.stdout = self.devices(*devices)
            with self.subTest(devices=devices), patch.object(prepare.subprocess, "Popen") as popen:
                with self.assertRaises(ValueError):
                    prepare.start(self.path)
                popen.assert_not_called()
            self.assertFalse(self.path.exists())

    def test_existing_ownership_is_never_replaced(self):
        self.state()
        with self.assertRaises(ValueError):
            prepare.start(self.path)
        self.command.assert_not_called()

    def test_invalid_device_is_rejected_before_state_or_worker_creation(self):
        self.command.return_value.stdout = self.devices(self.phone(device="all"))
        with patch.object(prepare.subprocess, "Popen") as popen, self.assertRaises(ValueError):
            prepare.start(self.path)
        popen.assert_not_called()
        self.assertFalse(self.path.exists())

    def test_start_cli_exports_only_validated_device(self):
        self.command.return_value.stdout = self.devices(self.phone("Booted"))
        with patch.dict(os.environ, self.environment, clear=True), patch.object(sys, "argv",
                ["prepare", "start", "--state", str(self.path)]):
            prepare.main()
        self.assertEqual((self.root / "output").read_text(), f"device={self.device}\n")

    def test_ready_can_boot_after_failed_preparation_and_is_bounded(self):
        self.state()
        prepare.ready(self.path)
        self.command.assert_called_once_with("bootstatus", self.device, "-b", timeout=180)

    def test_readiness_timeout_is_not_reported_as_success(self):
        self.state()
        self.command.side_effect = subprocess.TimeoutExpired("simctl", 180)
        with self.assertRaises(subprocess.TimeoutExpired):
            prepare.ready(self.path)

    def test_worker_boot_is_bounded_and_does_not_change_ownership(self):
        self.state(worker_pid=123)
        before = self.path.read_bytes()
        prepare.boot(self.path)
        self.command.assert_called_once_with("boot", self.device, timeout=120, check=False)
        self.assertEqual(self.path.read_bytes(), before)
        self.assertEqual(json.loads(self.path.with_suffix(".result.json").read_text()), {"exitCode": 0})

    def test_worker_records_failure_and_timeout_without_claiming_transport_success(self):
        self.state()
        for timeout in (False, True):
            with self.subTest(timeout=timeout):
                self.command.side_effect = subprocess.TimeoutExpired("simctl", 120) if timeout else None
                self.command.return_value.returncode = 1
                prepare.boot(self.path)
                self.assertEqual(json.loads(self.path.with_suffix(".result.json").read_text()),
                                 {"exitCode": 124 if timeout else 1})

    def test_retry_resets_only_selected_device_and_claims_cleanup_ownership(self):
        self.state(owned=False, worker_pid=123)
        with patch.object(prepare, "stop_worker") as stop:
            prepare.reset(self.path)
        stop.assert_called_once()
        self.assertEqual(self.command.call_args_list,
                         [call("shutdown", self.device, check=False), call("erase", self.device, timeout=60)])
        self.assertEqual(prepare.load(self.path), {"schema": 1, "device": self.device, "owned": True})

    def test_retry_erase_failure_blocks_retry(self):
        self.state()
        self.command.side_effect = [Mock(returncode=0), subprocess.CalledProcessError(1, "erase")]
        with patch.object(prepare, "stop_worker"), self.assertRaises(subprocess.CalledProcessError):
            prepare.reset(self.path)

    def test_finish_after_failed_sdk_setup_shuts_down_owned_device(self):
        self.state(worker_pid=123)
        with patch.object(prepare, "stop_worker") as stop:
            prepare.finish(self.path)
        stop.assert_called_once()
        self.command.assert_called_once_with("shutdown", self.device, check=False)

    def test_finish_still_attempts_shutdown_when_worker_cleanup_fails(self):
        self.state(worker_pid=123)
        with patch.object(prepare, "stop_worker", side_effect=OSError("ps failed")), self.assertRaises(OSError):
            prepare.finish(self.path)
        self.command.assert_called_once_with("shutdown", self.device, check=False)

    def test_finish_does_not_touch_a_borrowed_device_or_missing_state(self):
        prepare.finish(self.path)
        self.state(owned=False)
        with patch.object(prepare, "stop_worker"):
            prepare.finish(self.path)
        self.command.assert_not_called()

    def test_failed_shutdown_only_accepts_confirmed_shutdown_state(self):
        self.state()
        for state in ("Booted", "Shutdown", "missing"):
            self.command.reset_mock()
            self.command.side_effect = [Mock(returncode=1), Mock(stdout=self.devices(
                *([] if state == "missing" else [self.phone(state)])))]
            with self.subTest(state=state), patch.object(prepare, "stop_worker"):
                if state == "Shutdown":
                    prepare.finish(self.path)
                else:
                    with self.assertRaises(ValueError):
                        prepare.finish(self.path)
            self.assertEqual(self.command.call_args_list,
                             [call("shutdown", self.device, check=False),
                              call("list", "devices", "available", "--json")])

    def test_invalid_state_cannot_reset_or_shutdown_any_device(self):
        self.path.parent.mkdir(parents=True)
        for state in ({}, [], {"schema": 1, "device": "all", "owned": True},
                      {"schema": 1, "device": 123, "owned": True},
                      {"schema": 1, "device": self.device, "owned": "true"}):
            self.path.write_text(json.dumps(state))
            for operation in (prepare.ready, prepare.reset, prepare.finish):
                with self.subTest(state=state, operation=operation.__name__), self.assertRaises(ValueError):
                    operation(self.path)
        self.command.assert_not_called()

    def test_reused_pid_is_not_killed(self):
        self.state(worker_pid=123)
        with patch.object(prepare.subprocess, "run", return_value=Mock(returncode=0, stdout="other process")), \
                patch.object(prepare.os, "killpg") as kill:
            prepare.stop_worker(self.path, prepare.load(self.path))
        kill.assert_not_called()

    def test_matching_worker_requires_its_script_state_and_pid(self):
        expected = f"python3 {Path(prepare.__file__).resolve()} _boot --state {self.path}"
        with patch.object(prepare.subprocess, "run", return_value=Mock(returncode=0, stdout=expected)) as ps:
            self.assertTrue(prepare.matching_worker(self.path, 123))
            self.assertFalse(prepare.matching_worker(self.path.with_name("other.json"), 123))
            for pid in (None, 1, "123", True):
                self.assertFalse(prepare.matching_worker(self.path, pid))
        self.assertEqual(ps.call_count, 2)
        self.assertEqual(ps.call_args.kwargs["timeout"], 5)

    def test_owned_worker_group_receives_bounded_term_then_kill_if_needed(self):
        self.state(worker_pid=123)
        with patch.object(prepare, "matching_worker", return_value=True), \
                patch.object(prepare.os, "getpgid", return_value=123), \
                patch.object(prepare, "wait_group", side_effect=[False, True]) as wait, \
                patch.object(prepare.os, "killpg") as kill:
            prepare.stop_worker(self.path, prepare.load(self.path))
        self.assertEqual(kill.call_args_list, [call(123, signal.SIGTERM), call(123, signal.SIGKILL)])
        self.assertEqual(wait.call_args_list, [call(123, 5), call(123, 2)])

    def test_worker_exiting_after_term_needs_no_kill(self):
        self.state(worker_pid=123)
        with patch.object(prepare, "matching_worker", return_value=True), \
                patch.object(prepare.os, "getpgid", return_value=123), \
                patch.object(prepare, "wait_group", return_value=True), \
                patch.object(prepare.os, "killpg") as kill:
            prepare.stop_worker(self.path, prepare.load(self.path))
        kill.assert_called_once_with(123, signal.SIGTERM)

    def test_worker_cannot_signal_a_group_it_does_not_own(self):
        self.state(worker_pid=123)
        with patch.object(prepare, "matching_worker", return_value=True), \
                patch.object(prepare.os, "getpgid", return_value=456), \
                patch.object(prepare.os, "killpg") as kill, self.assertRaises(ValueError):
            prepare.stop_worker(self.path, prepare.load(self.path))
        kill.assert_not_called()

    def test_stubborn_group_cleanup_failure_blocks_success(self):
        self.state(worker_pid=123)
        with patch.object(prepare, "matching_worker", return_value=True), \
                patch.object(prepare.os, "getpgid", return_value=123), \
                patch.object(prepare, "wait_group", return_value=False), \
                patch.object(prepare.os, "killpg"), self.assertRaises(ValueError):
            prepare.stop_worker(self.path, prepare.load(self.path))

    def test_group_probe_is_bounded_even_if_leader_exited_but_child_remains(self):
        with patch.object(prepare.os, "killpg") as kill, \
                patch.object(prepare.time, "monotonic", side_effect=[0, 1, 6]), \
                patch.object(prepare.time, "sleep") as sleep:
            self.assertFalse(prepare.wait_group(123, 5))
        self.assertEqual(kill.call_args_list, [call(123, 0), call(123, 0)])
        sleep.assert_called_once_with(0.1)
        with patch.object(prepare.os, "killpg", side_effect=ProcessLookupError):
            self.assertTrue(prepare.wait_group(123, 5))


class NativeCommandTests(unittest.TestCase):
    def test_native_commands_have_a_finite_default_timeout(self):
        with patch.object(prepare.subprocess, "run") as run:
            prepare.command("list", "devices", "available", "--json")
        run.assert_called_once_with(["xcrun", "simctl", "list", "devices", "available", "--json"],
                                    check=True, timeout=20, stdout=subprocess.PIPE,
                                    stderr=subprocess.STDOUT, text=True)


if __name__ == "__main__":
    unittest.main()
