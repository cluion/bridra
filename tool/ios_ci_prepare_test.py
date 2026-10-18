"""Fake-only concurrency, evidence and owned-cleanup contracts."""
import hashlib
import json
import os
from pathlib import Path
import signal
import shutil
import subprocess
import sys
import tempfile
import threading
import time
import unittest
from unittest.mock import Mock, call, patch

import ios_ci_prepare as prepare


class ConcurrentPreparationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.path = self.root / "preparation/pipeline.json"
        self.path.parent.mkdir()
        self.source = {"head": "a" * 40, "root": str(self.root), "developerDirectory": "/fixed/Xcode"}
        self.environment = {"GITHUB_ACTIONS": "true", "RUNNER_ENVIRONMENT": "github-hosted",
                            "RUNNER_TEMP": str(self.root)}
        for mock in (patch.object(prepare, "ROOT", self.root),
                     patch.object(prepare, "identity", return_value=self.source)):
            mock.start()
            self.addCleanup(mock.stop)
        self.backend = self.root / "build/server/bridra_server"
        self.backend.parent.mkdir(parents=True)
        self.backend.write_bytes(b"verified backend")
        self.backend.chmod(0o755)

    def state(self):
        value = {"schema": 1, "pid": 123, "token": "b" * 32, "source": self.source}
        prepare.simulator.save(self.path, value)
        return value

    def result(self, **overrides):
        state = self.state()
        value = {"pid": state["pid"], "token": state["token"], "source": state["source"],
                 "stages": {name: {"exit": 0, "seconds": 1.25} for name in prepare.STAGES},
                 "backendSHA256": hashlib.sha256(self.backend.read_bytes()).hexdigest(), **overrides}
        prepare.simulator.save(self.path.with_suffix(".result.json"), value)
        return value

    def test_every_cli_rejects_local_or_self_hosted_use_before_work(self):
        for operation in ("begin", "ready", "verify", "reset", "finish", "_work"):
            with self.subTest(operation=operation), patch.dict(os.environ, {}, clear=True), \
                    patch.object(sys, "argv", ["prepare", operation, "--state", str(self.path)]), \
                    patch.object(prepare.subprocess, "Popen") as popen:
                with self.assertRaises(ValueError):
                    prepare.main()
                popen.assert_not_called()

    def test_begin_records_source_and_exact_detached_worker_identity(self):
        with patch.object(prepare.subprocess, "Popen", return_value=Mock(pid=123)) as popen:
            prepare.begin(self.path)
        state = prepare.load(self.path)
        self.assertEqual(state["source"], self.source)
        self.assertEqual(state["pid"], 123)
        self.assertEqual(len(state["token"]), 32)
        self.assertEqual(popen.call_args.args[0][-4:], ["--state", str(self.path), "--token", state["token"]])
        self.assertTrue(popen.call_args.kwargs["start_new_session"])
        self.assertEqual(popen.call_args.kwargs["stdin"], subprocess.DEVNULL)
        with patch.object(prepare.subprocess, "Popen") as again, self.assertRaises(ValueError):
            prepare.begin(self.path)
        again.assert_not_called()

    def test_stage_commands_keep_strict_native_readiness_and_all_prerequisites(self):
        commands = prepare.commands(self.path)
        self.assertEqual(commands["xcode-check"], (["make", "macos-check"], 180))
        self.assertEqual(commands["backend-build"], (["make", "backend-server-build"], 180))
        self.assertEqual(commands["simulator-ready"][0][-3:],
                         ["ready", "--state", str(prepare.native_state(self.path))])
        self.assertEqual(commands["simulator-ready"][1], 330)

    def test_all_three_stages_actually_overlap_and_bind_the_built_backend(self):
        state = self.state()
        barrier = threading.Barrier(3)
        def stage(path, name, args, timeout):
            barrier.wait(timeout=3)
            return {"exit": 0, "seconds": 2.5}
        with patch.object(prepare.os, "getpid", return_value=123), patch.object(prepare, "run_stage", side_effect=stage):
            prepare.work(self.path, state["token"])
        value = prepare.result(self.path)
        self.assertEqual(set(value["stages"]), set(prepare.STAGES))
        self.assertEqual(value["backendSHA256"], hashlib.sha256(self.backend.read_bytes()).hexdigest())

    def test_failure_and_timeout_cannot_be_consumed_as_prepared_success(self):
        for stage in prepare.STAGES:
            for status in (1, 65, 124):
                value = self.result()
                value["stages"][stage]["exit"] = status
                prepare.simulator.save(self.path.with_suffix(".result.json"), value)
                with self.subTest(stage=stage, status=status), self.assertRaises(subprocess.CalledProcessError):
                    prepare.result(self.path)

    def test_stage_elapsed_limit_preserves_failure_and_uses_no_new_session(self):
        with patch.object(prepare.subprocess, "run", side_effect=subprocess.TimeoutExpired("fake", 180)) as run:
            value = prepare.run_stage(self.path, "xcode-check", ["fake"], 180)
        self.assertEqual(value["exit"], 124)
        self.assertEqual(run.call_args.kwargs["timeout"], 180)
        self.assertNotIn("start_new_session", run.call_args.kwargs)

    def test_source_toolchain_worker_and_backend_mismatch_block_consumption(self):
        for key, value in (("pid", 456), ("token", "c" * 32), ("source", {**self.source, "head": "c" * 40}),
                           ("stages", {}), ("backendSHA256", "0" * 64)):
            self.result(**{key: value})
            with self.subTest(key=key), self.assertRaises(ValueError):
                prepare.result(self.path)
        self.result()
        self.backend.write_bytes(b"changed")
        with self.assertRaises(ValueError):
            prepare.result(self.path)
        self.result()
        self.backend.chmod(0o644)
        with self.assertRaises(ValueError):
            prepare.result(self.path)

    def test_malformed_ownership_is_never_a_signal_target(self):
        for overrides in ({"pid": True}, {"pid": 1}, {"token": None}, {"source": {"head": "b" * 40}}):
            value = self.state()
            prepare.simulator.save(self.path, {**value, **overrides})
            with self.subTest(overrides=overrides), patch.object(prepare.os, "killpg") as kill, self.assertRaises(ValueError):
                prepare.finish(self.path)
            kill.assert_not_called()

    def test_ready_joins_worker_and_checks_every_result_without_native_commands(self):
        self.result()
        with patch.object(prepare, "stop") as stop, patch.object(prepare.subprocess, "run") as run:
            prepare.ready(self.path)
        stop.assert_called_once_with(self.path, prepare.load(self.path))
        run.assert_not_called()

    def test_readiness_timeout_still_stops_owned_group(self):
        self.state()
        with patch.object(prepare.time, "monotonic", side_effect=[0, 360]), \
                patch.object(prepare, "stop") as stop, self.assertRaises(TimeoutError):
            prepare.ready(self.path)
        stop.assert_called_once()

    def test_reused_pid_cannot_be_killed_or_allow_a_reset(self):
        self.state()
        with patch.object(prepare, "matching", return_value=False), \
                patch.object(prepare.simulator, "wait_group", return_value=False), \
                patch.object(prepare.os, "killpg") as kill, patch.object(prepare.simulator, "reset") as reset, \
                self.assertRaises(ValueError):
            prepare.finish(self.path, reset=True)
        kill.assert_not_called()
        reset.assert_not_called()

    def test_owned_group_is_stopped_before_targeted_reset_or_finish(self):
        self.state()
        for reset in (True, False):
            order = []
            with patch.object(prepare, "stop", side_effect=lambda *a: order.append("stop")), \
                    patch.object(prepare.simulator, "reset", side_effect=lambda *a: order.append("reset")), \
                    patch.object(prepare.simulator, "finish", side_effect=lambda *a: order.append("finish")):
                prepare.finish(self.path, reset=reset)
            self.assertEqual(order, ["stop", "reset" if reset else "finish"])

    def test_owned_group_escalates_bounded_term_to_kill(self):
        state = self.state()
        with patch.object(prepare, "matching", return_value=True), \
                patch.object(prepare.os, "getpgid", return_value=123), \
                patch.object(prepare.simulator, "wait_group", side_effect=[False, True]), \
                patch.object(prepare.os, "killpg") as kill:
            prepare.stop(self.path, state)
        self.assertEqual(kill.call_args_list, [call(123, signal.SIGTERM), call(123, signal.SIGKILL)])

    def test_mismatched_simulator_cannot_consume_prepared_binary(self):
        self.result()
        prepare.simulator.save(prepare.native_state(self.path),
                               {"schema": 1, "device": "11111111-2222-3333-4444-555555555555", "owned": True})
        with patch.dict(os.environ, self.environment, clear=True), \
                patch.object(sys, "argv", ["prepare", "verify", "--state", str(self.path), "--device", "other"]), \
                self.assertRaises(ValueError):
            prepare.main()


class FakeProcessIntegrationTests(unittest.TestCase):
    """Exercise real owned groups using fake native commands in a scratch tree."""

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        (self.root / "tool").mkdir()
        self.bin = self.root / "bin"
        self.bin.mkdir()
        for name in ("ios_ci_prepare.py", "ios_simulator_prepare.py"):
            shutil.copyfile(Path(__file__).with_name(name), self.root / "tool" / name)
        self.path = self.root / "owned/pipeline.json"
        self.environment = dict(os.environ, GITHUB_ACTIONS="true", RUNNER_ENVIRONMENT="github-hosted",
                                RUNNER_TEMP=str(self.root), PATH=str(self.bin), DEVELOPER_DIR="/fake/Xcode",
                                PYTHONDONTWRITEBYTECODE="1")
        (self.bin / "ps").symlink_to(shutil.which("ps"))
        self.executable("git", 'print("a" * 40)')
        self.executable("make", '''
import os, pathlib, sys, time
root = pathlib.Path.cwd()
(root / (sys.argv[1] + ".started")).touch()
if sys.argv[1] == "macos-check" and os.environ.get("FAKE_HANG") == "1":
    time.sleep(60)
if sys.argv[1] == "backend-server-build":
    server = root / "build/server/bridra_server"
    server.parent.mkdir(parents=True, exist_ok=True)
    server.write_bytes(b"fake executable")
    server.chmod(0o755)
''')
        self.executable("xcrun", '''
import json, pathlib, sys
root = pathlib.Path.cwd()
with (root / "native-calls").open("a") as output:
    output.write(" ".join(sys.argv[1:]) + "\\n")
if sys.argv[2] == "list":
    print(json.dumps({"devices": {"fake": [{"name": "iPhone fake", "udid": "11111111-2222-3333-4444-555555555555", "state": "Shutdown", "isAvailable": True}]}}))
''')
        self.addCleanup(self.cleanup_worker)

    def executable(self, name, source):
        path = self.bin / name
        path.write_text(f"#!{sys.executable}\n" + source + "\n")
        path.chmod(0o755)

    def cli(self, operation, *args):
        return subprocess.run([sys.executable, str(self.root / "tool/ios_ci_prepare.py"), operation,
                               "--state", str(self.path), *args], cwd=self.root, env=self.environment,
                              capture_output=True, text=True, timeout=20)

    def cleanup_worker(self):
        if self.path.exists():
            result = self.cli("finish")
            if result.returncode:
                raise AssertionError(result.stdout + result.stderr)

    def test_real_detached_worker_joins_verifies_and_cleans_fake_simulator(self):
        for operation, args in (("begin", ()), ("ready", ()),
                                ("verify", ("--device", "11111111-2222-3333-4444-555555555555")),
                                ("finish", ())):
            result = self.cli(operation, *args)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        calls = (self.root / "native-calls").read_text()
        self.assertEqual(calls.count("simctl bootstatus "), 1)
        self.assertIn("simctl shutdown 11111111-2222-3333-4444-555555555555", calls)

    def test_setup_cancellation_stops_live_group_before_native_cleanup(self):
        self.environment["FAKE_HANG"] = "1"
        started = self.cli("begin")
        self.assertEqual(started.returncode, 0, started.stderr)
        until = time.monotonic() + 5
        while not (self.root / "macos-check.started").exists() or not self.path.with_name("simulator.json").exists():
            if time.monotonic() >= until:
                self.fail("fake workers did not start")
            time.sleep(0.05)
        result = self.cli("finish")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        pid = json.loads(self.path.read_text())["pid"]
        with self.assertRaises(ProcessLookupError):
            os.killpg(pid, 0)


if __name__ == "__main__":
    unittest.main()
