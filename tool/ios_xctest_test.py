from pathlib import Path
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

import ios_xctest


class XCTestResultTests(unittest.TestCase):
    def summary(self):
        return {"totalTestCount": 1, "passedTests": 1, "failedTests": 0,
                "skippedTests": 0, "expectedFailures": 0, "testFailures": []}

    def test_complete_positive_run_passes(self):
        ios_xctest.validate_results(self.summary(), 0, False)

    def test_zero_tests_skips_extra_tests_and_hidden_failures_block(self):
        for key, value in (("totalTestCount", 0), ("totalTestCount", 2), ("skippedTests", 1),
                           ("expectedFailures", 1), ("failedTests", 1), ("passedTests", 0)):
            with self.subTest(key=key, value=value):
                summary = self.summary()
                summary[key] = value
                with self.assertRaises(ValueError):
                    ios_xctest.validate_results(summary, 0, False)
        with self.assertRaises(ValueError):
            ios_xctest.validate_results(self.summary(), 65, False)
        for invalid in (True, None, "1"):
            summary = self.summary()
            summary["totalTestCount"] = invalid
            with self.assertRaises(ValueError):
                ios_xctest.validate_results(summary, 0, False)

    def test_only_the_intentional_dart_failure_satisfies_the_negative_contract(self):
        summary = self.summary()
        summary.update(passedTests=0, failedTests=1,
                       testFailures=[{"failureText": ios_xctest.FAILURE_MARKER}])
        ios_xctest.validate_results(summary, 65, True)
        for status, failure in ((0, ios_xctest.FAILURE_MARKER), (1, ios_xctest.FAILURE_MARKER),
                                (65, "Simulator failed to boot"), (65, "")):
            with self.subTest(status=status, failure=failure):
                summary["testFailures"] = [{"failureText": failure}]
                with self.assertRaises(ValueError):
                    ios_xctest.validate_results(summary, status, True)
        summary["testFailures"] = [{"failureText": ios_xctest.FAILURE_MARKER},
                                   {"failureText": "unrelated native assertion"}]
        with self.assertRaises(ValueError):
            ios_xctest.validate_results(summary, 65, True)

    def test_every_existing_backend_receipt_is_required(self):
        complete = "\n".join(ios_xctest.RECEIPTS)
        ios_xctest.validate_receipts(complete)
        for missing in ios_xctest.RECEIPTS:
            with self.subTest(missing=missing):
                with self.assertRaises(ValueError):
                    ios_xctest.validate_receipts(complete.replace(missing, ""))


class XCTestIdentityTests(unittest.TestCase):
    def test_artifact_identity_tracks_sources_and_ignores_removed_paths(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "app.dart").write_text("first source")
            with patch("ios_xctest.subprocess.check_output", return_value=b"removed\0app.dart\0"):
                first = ios_xctest.source_identity(root)
                (root / "app.dart").write_text("second source")
                self.assertNotEqual(first, ios_xctest.source_identity(root))


class XCTestDeadlineTests(unittest.TestCase):
    def test_hung_stage_has_a_real_elapsed_deadline_and_reaps_its_process(self):
        with tempfile.TemporaryDirectory() as directory:
            experiment = ios_xctest.Experiment(Path(directory), Path(directory) / "logs")
            started = time.monotonic()
            with self.assertRaises(TimeoutError):
                experiment.run("hung", [sys.executable, "-c", "import time; time.sleep(30)"], timeout=0.1)
            self.assertLess(time.monotonic() - started, 3)
            self.assertTrue(all(process.poll() is not None for process in experiment.processes))


if __name__ == "__main__":
    unittest.main()
