import contextlib
import hashlib
import io
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch
import warnings
import zipfile

import ci_fvm


class WindowsFVMTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.environment = {"RUNNER_OS": "Windows", "RUNNER_ARCH": "X64",
                            "RUNNER_TEMP": str(self.root), "GITHUB_PATH": str(self.root / 'path')}

    def archive(self, names=ci_fvm.MEMBERS):
        output = io.BytesIO()
        with zipfile.ZipFile(output, "w") as archive, warnings.catch_warnings():
            warnings.simplefilter('ignore', UserWarning)
            for name in names:
                archive.writestr(name, b'fake executable' if name.endswith('.exe') else b'license')
        return output.getvalue()

    def run_install(self, blob, version=ci_fvm.VERSION, **checks):
        with patch('ci_fvm.urllib.request.urlopen', return_value=io.BytesIO(blob)) as download, \
                patch.dict(ci_fvm.WINDOWS_ARCHIVES, {self.environment['RUNNER_ARCH']: hashlib.sha256(blob).hexdigest()}), \
                patch('ci_fvm.subprocess.check_output', return_value=version, **checks) as execute, \
                contextlib.redirect_stdout(io.StringIO()):
            ci_fvm.install(self.environment)
        return download, execute

    def test_exact_archive_and_version_export_only_verified_binary(self):
        for arch in ('X64',):
            with self.subTest(architecture=arch):
                self.environment['RUNNER_ARCH'] = arch
                download, execute = self.run_install(self.archive())
                self.assertEqual(download.call_args.args[0],
                                 f'https://github.com/btwld/fvm/releases/download/4.1.2/fvm-4.1.2-windows-{arch.lower()}.zip')
                self.assertEqual(download.call_args.kwargs['timeout'], 30)
                binary, option = execute.call_args.args[0]
                self.assertEqual(option, '--version')
                self.assertEqual(execute.call_args.kwargs['timeout'], 15)
                binary = Path(binary)
                self.assertEqual(binary.read_bytes(), b'fake executable')
                self.assertEqual((binary.parent / 'src/LICENSE').read_bytes(), b'license')
                self.assertIn(str(binary.parent), (self.root / 'path').read_text().splitlines())

    def test_unknown_host_and_architecture_fail_before_download(self):
        for key, value in [('RUNNER_OS', 'Linux'), ('RUNNER_ARCH', 'unknown'), ('RUNNER_ARCH', 'ARM64')]:
            with self.subTest(key=key):
                environment = dict(self.environment, **{key: value})
                with patch('ci_fvm.urllib.request.urlopen') as download, self.assertRaises(ValueError):
                    ci_fvm.install(environment)
                download.assert_not_called()

    def test_hash_mismatch_never_extracts_executes_or_exports(self):
        with patch('ci_fvm.urllib.request.urlopen', return_value=io.BytesIO(self.archive())), \
                patch('ci_fvm.subprocess.check_output') as execute, \
                self.assertRaisesRegex(ValueError, 'SHA-256'):
            ci_fvm.install(self.environment)
        execute.assert_not_called()
        self.assertEqual(list(self.root.iterdir()), [])

    def test_oversized_download_is_rejected_before_installation(self):
        with patch('ci_fvm.MAX_ARCHIVE_BYTES', 5), \
                patch('ci_fvm.urllib.request.urlopen', return_value=io.BytesIO(b'123456')), \
                self.assertRaisesRegex(ValueError, 'download limit'):
            ci_fvm.install(self.environment)
        self.assertEqual(list(self.root.iterdir()), [])

    def test_unexpected_missing_duplicate_and_traversal_members_fail_closed(self):
        for names in [('fvm/fvm.exe',), (*ci_fvm.MEMBERS, '../unexpected'),
                      (*ci_fvm.MEMBERS, 'fvm/src/LICENSE'), ('../fvm.exe', 'fvm/src/LICENSE')]:
            with self.subTest(names=names):
                with self.assertRaisesRegex(ValueError, 'contents'):
                    self.run_install(self.archive(names))
                self.assertEqual(list(self.root.iterdir()), [])

    def test_extraction_size_limit_preserves_runner_files(self):
        existing = self.root / 'unrelated'
        existing.write_text('preserve')
        with patch('ci_fvm.MAX_EXTRACTED_BYTES', 1), self.assertRaisesRegex(ValueError, 'extraction limit'):
            self.run_install(self.archive())
        self.assertEqual(existing.read_text(), 'preserve')
        self.assertEqual(list(self.root.iterdir()), [existing])

    def test_wrong_version_failed_execution_and_timeout_never_export(self):
        cases = [('different', {}), (ci_fvm.VERSION, {'side_effect': subprocess.CalledProcessError(1, 'fvm')}),
                 (ci_fvm.VERSION, {'side_effect': subprocess.TimeoutExpired('fvm', 15)})]
        for version, checks in cases:
            with self.subTest(version=version, checks=checks):
                with self.assertRaises((ValueError, subprocess.CalledProcessError, subprocess.TimeoutExpired)):
                    self.run_install(self.archive(), version, **checks)
                self.assertEqual(list(self.root.iterdir()), [])

    def test_export_failure_removes_only_owned_installation(self):
        with patch('ci_fvm.open', side_effect=OSError('export failed')), self.assertRaises(OSError):
            self.run_install(self.archive())
        self.assertEqual(list(self.root.iterdir()), [])


if __name__ == '__main__':
    unittest.main()
