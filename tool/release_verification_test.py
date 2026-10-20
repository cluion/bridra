"""Contracts for source-equivalent verification reuse and publication gating."""

import copy
from datetime import datetime, timedelta, timezone
import hashlib
import io
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch
import zipfile

import ci
import release_verification as release


class EvidenceTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.now = datetime(2026, 10, 18, tzinfo=timezone.utc)
        self.repo, self.repo_id = 'owner/bridra', 42
        self.base, self.head, self.tested = 'a' * 40, 'b' * 40, 'c' * 40
        subprocess.run(['git', 'init', '-q', str(self.root)], check=True)
        (self.root / 'content').write_text('exact reviewed contents')
        subprocess.run(['git', 'add', 'content'], cwd=self.root, check=True)
        subprocess.run(['git', '-c', 'user.name=Test', '-c', 'user.email=test@example.test',
                        'commit', '-qm', 'test source'], cwd=self.root, check=True)
        self.tag = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=self.root, text=True).strip()
        self.tree = subprocess.check_output(['git', 'rev-parse', 'HEAD^{tree}'], cwd=self.root, text=True).strip()
        self.pull = {'number': 7, 'merged': True, 'state': 'closed', 'merge_commit_sha': self.tag,
                     'base': {'ref': 'main', 'sha': self.base, 'repo': {'id': self.repo_id}},
                     'head': {'sha': self.head, 'repo': {'id': self.repo_id}}}
        self.run = {'id': 80, 'run_attempt': 1, 'workflow_id': 12, 'head_sha': self.head,
                    'path': release.WORKFLOW, 'status': 'completed', 'conclusion': 'success',
                    'event': 'pull_request', 'repository': {'id': self.repo_id, 'full_name': self.repo},
                    'head_repository': {'id': self.repo_id}, 'created_at': self.now.isoformat()}
        self.proof = {'schema': 1, 'repository_id': self.repo_id, 'repository': self.repo,
                      'workflow': release.WORKFLOW, 'run_id': 80, 'run_attempt': 1,
                      'event': 'pull_request', 'pull_request': 7, 'base_sha': self.base,
                      'head_sha': self.head, 'tested_sha': self.tested, 'tree': self.tree,
                      'plan': {key: True for key in ci.SCOPES}}
        self.jobs = [{'name': name, 'run_id': 80, 'head_sha': self.head,
                      'status': 'completed', 'conclusion': 'success'} for name in release.REQUIRED_JOBS]
        self.jobs[-1]['steps'] = [{'name': f'Record complete PR verification evidence for {self.tested}',
                                   'status': 'completed', 'conclusion': 'success'}]
        self.artifact = {'id': 91, 'name': 'pr-verification-80-1', 'expired': False,
                         'workflow_run': {'id': 80, 'head_sha': self.head}}
        self.commit = {'sha': self.tested, 'tree': {'sha': self.tree},
                       'parents': [{'sha': self.base}, {'sha': self.head}]}
        self.calls = []

    def archive(self, name='verification.json', data=None):
        handle = io.BytesIO()
        with zipfile.ZipFile(handle, 'w', zipfile.ZIP_DEFLATED) as archive:
            archive.writestr(name, json.dumps(self.proof) if data is None else data)
        return handle.getvalue()

    def read(self, path, binary=False):
        self.calls.append(path)
        responses = {
            f'commits/{self.tag}/pulls?per_page=100': [self.pull], 'pulls/7': self.pull,
            'actions/workflows/verify.yml': {'id': 12, 'path': release.WORKFLOW, 'state': 'active'},
            f'actions/workflows/12/runs?event=pull_request&head_sha={self.head}&per_page=100':
                {'total_count': 1, 'workflow_runs': [self.run]},
            'actions/runs/80': self.run,
            'actions/runs/80/attempts/1/jobs?per_page=100': {'total_count': len(self.jobs), 'jobs': self.jobs},
            'actions/runs/80/artifacts?per_page=100': {'total_count': 1, 'artifacts': [self.artifact]},
            f'git/commits/{self.tested}': self.commit,
        }
        if path == 'actions/artifacts/91/zip':
            self.assertTrue(binary)
            return self.data
        return copy.deepcopy(responses[path])

    def select(self):
        self.data = self.archive()
        self.artifact.update(size_in_bytes=len(self.data), digest='sha256:' + hashlib.sha256(self.data).hexdigest())
        return release.select(self.root, self.repo, self.repo_id, self.tag, self, self.now)

    def test_squash_commit_with_identical_tree_can_reuse_full_verification(self):
        result = self.select()
        self.assertNotEqual(self.tag, self.tested)
        self.assertTrue(result['reuse'])
        self.assertEqual(result['release_sha'], self.tag)
        self.assertEqual(result['tree'], self.tree)
        self.assertEqual(self.calls.count('actions/runs/80'), 2)

    def test_each_required_job_must_be_unique_complete_and_successful(self):
        for index, name in enumerate(release.REQUIRED_JOBS):
            for result in ('failure', 'cancelled', 'skipped', None):
                with self.subTest(job=name, result=result):
                    original = self.jobs[index]['conclusion']
                    self.jobs[index]['conclusion'] = result
                    with self.assertRaises(ValueError): self.select()
                    self.jobs[index]['conclusion'] = original
        self.jobs.append(copy.deepcopy(self.jobs[0]))
        with self.assertRaises(ValueError): self.select()
        self.jobs = self.jobs[1:-1]
        with self.assertRaises(ValueError): self.select()

    def test_passing_group_gates_cannot_replace_missing_shard_evidence(self):
        complete = self.jobs
        shards = {"checks / " + job for jobs in ci.GROUPS.values() for job in jobs}
        for missing in ({job} for job in shards):
            with self.subTest(missing=missing):
                self.jobs = [job for job in complete if job['name'] not in missing]
                with self.assertRaises(ValueError): self.select()
        # Records from the former serial graph are insufficient for this graph.
        self.jobs = [job for job in complete if job['name'] not in shards]
        with self.assertRaises(ValueError): self.select()

    def test_partial_or_malformed_plan_never_substitutes_for_full_checks(self):
        for scope in ci.SCOPES:
            self.proof['plan'][scope] = False
            with self.subTest(scope=scope), self.assertRaises(ValueError): self.select()
            self.proof['plan'][scope] = True
        self.proof['plan'] = {}
        with self.assertRaises(ValueError): self.select()

    def test_wrong_record_source_identity_or_attempt_rejected(self):
        for key, value in (('schema', 2), ('repository_id', 43), ('repository', 'other/repo'),
                           ('workflow', 'other.yml'), ('event', 'push'), ('pull_request', 8),
                           ('run_id', 81), ('run_attempt', 2), ('head_sha', 'd' * 40),
                           ('tree', 'e' * 40), ('base_sha', 'f' * 40)):
            with self.subTest(key=key):
                original = self.proof[key]
                self.proof[key] = value
                with self.assertRaises(ValueError): self.select()
                self.proof[key] = original

    def test_commit_tree_and_merge_parents_are_independently_verified(self):
        for field, value in (('tree', {'sha': 'd' * 40}), ('sha', 'e' * 40),
                             ('parents', [{'sha': self.head}]),
                             ('parents', [{'sha': self.base}, {'sha': 'e' * 40}])):
            original = self.commit[field]
            self.commit[field] = value
            with self.subTest(field=field), self.assertRaises(ValueError): self.select()
            self.commit[field] = original

    def test_record_merge_sha_must_match_github_evaluated_successful_gate_step(self):
        step = self.jobs[-1]['steps'][0]
        for field, value in (('name', 'Record complete PR verification evidence for ' + 'd' * 40),
                             ('conclusion', 'failure'), ('status', 'in_progress')):
            original = step[field]
            step[field] = value
            with self.subTest(field=field), self.assertRaises(ValueError): self.select()
            step[field] = original
        self.jobs[-1]['steps'] = []
        with self.assertRaises(ValueError): self.select()

    def test_changed_repository_content_blocks_even_if_commit_metadata_is_unchanged(self):
        (self.root / 'content').write_text('different release content')
        subprocess.run(['git', 'add', 'content'], cwd=self.root, check=True)
        subprocess.run(['git', '-c', 'user.name=Test', '-c', 'user.email=test@example.test',
                        'commit', '-qm', 'changed contents'], cwd=self.root, check=True)
        self.tag = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=self.root, text=True).strip()
        self.pull['merge_commit_sha'] = self.tag
        with self.assertRaises(ValueError): self.select()

    def test_pr_must_be_merged_same_repository_into_main(self):
        for path, value in ((('merged',), False), (('state',), 'open'), (('base', 'ref'), 'dev'),
                            (('base', 'repo', 'id'), 43), (('head', 'repo', 'id'), 43)):
            target = self.pull
            for key in path[:-1]: target = target[key]
            original = target[path[-1]]
            target[path[-1]] = value
            with self.subTest(path=path), self.assertRaises(ValueError): self.select()
            target[path[-1]] = original

    def test_latest_run_must_be_current_successful_pr_workflow(self):
        for key, value in (('conclusion', 'failure'), ('status', 'in_progress'), ('event', 'workflow_dispatch'),
                           ('path', 'fake.yml'), ('workflow_id', 13), ('head_sha', 'd' * 40),
                           ('head_repository', {'id': 43}), ('repository', {'id': 43, 'full_name': self.repo}),
                           ('created_at', (self.now - timedelta(days=8)).isoformat()),
                           ('created_at', (self.now + timedelta(seconds=1)).isoformat())):
            original = self.run[key]
            self.run[key] = value
            with self.subTest(key=key), self.assertRaises(ValueError): self.select()
            self.run[key] = original

    def test_rerun_started_during_lookup_invalidates_decision(self):
        read = self.read
        def changing(path, binary=False):
            result = read(path, binary)
            if path == 'actions/runs/80' and self.calls.count(path) == 2:
                result['run_attempt'] = 2
            return result
        with patch.object(self, 'read', side_effect=changing), self.assertRaises(ValueError): self.select()

    def test_newer_failed_run_cannot_be_bypassed_by_an_older_success(self):
        read = self.read
        newer = dict(self.run, id=81, conclusion='failure', created_at=(self.now + timedelta(seconds=1)).isoformat())
        def changing(path, binary=False):
            if path.startswith('actions/workflows/12/runs?'):
                return {'total_count': 2, 'workflow_runs': [self.run, newer]}
            if path == 'actions/runs/81':
                return newer
            return read(path, binary)
        with patch.object(self, 'read', side_effect=changing), self.assertRaises(ValueError): self.select()
        self.assertNotIn('actions/runs/80', self.calls)

    def test_new_run_created_during_lookup_invalidates_old_success(self):
        read = self.read
        calls = []
        def changing(path, binary=False):
            result = read(path, binary)
            if path.startswith('actions/workflows/12/runs?'):
                calls.append(path)
                if len(calls) == 2:
                    result['workflow_runs'] = [dict(self.run, id=81)]
            return result
        with patch.object(self, 'read', side_effect=changing), self.assertRaises(ValueError): self.select()

    def test_corrupt_expired_or_unrelated_artifacts_rejected(self):
        for key, value in (('expired', True), ('name', 'old-attempt'), ('workflow_run', {'id': 99, 'head_sha': self.head})):
            original = self.artifact[key]
            self.artifact[key] = value
            with self.subTest(key=key), self.assertRaises(ValueError): self.select()
            self.artifact[key] = original
        with self.assertRaises(ValueError): release.read_proof(self.archive(), {'digest': 'sha256:' + '0' * 64})
        for name, data in (('../verification.json', '{}'), ('verification.json', 'x' * (release.MAX_PROOF_BYTES + 1))):
            archive = self.archive(name, data)
            with self.subTest(name=name), self.assertRaises(ValueError):
                release.read_proof(archive, {'digest': 'sha256:' + hashlib.sha256(archive).hexdigest()})

    def test_incomplete_api_pages_do_not_silently_omit_a_failed_job(self):
        with self.assertRaises(ValueError): release.complete_page({'total_count': 101, 'jobs': self.jobs}, 'jobs')

    def test_missing_artifact_never_becomes_implicit_success(self):
        read = self.read
        def missing(path, binary=False):
            if path == 'actions/runs/80/artifacts?per_page=100':
                return {'total_count': 0, 'artifacts': []}
            return read(path, binary)
        with patch.object(self, 'read', side_effect=missing), self.assertRaises(ValueError): self.select()

    def test_dirty_or_different_checkout_is_not_source_equivalence(self):
        (self.root / 'content').write_text('uncommitted change')
        with self.assertRaises(subprocess.CalledProcessError): self.select()
        self.assertEqual(self.calls, [])
        with self.assertRaises(ValueError):
            release.select(self.root, self.repo, self.repo_id, self.tested, self, self.now)

    def test_api_lookup_is_read_only_and_bounded(self):
        with patch('release_verification.time.monotonic', side_effect=[0, 1]), \
                patch('release_verification.subprocess.check_output', return_value=b'{}') as command:
            release.GitHub(self.repo).read('actions/workflows/verify.yml')
        args, kwargs = command.call_args
        self.assertIn('GET', args[0])
        self.assertIn('github.com', args[0])
        self.assertLessEqual(kwargs['timeout'], 10)
        with patch('release_verification.time.monotonic', side_effect=[0, 46]), \
                patch('release_verification.subprocess.check_output') as command:
            with self.assertRaises(ValueError): release.GitHub(self.repo).read('actions/runs/80')
            command.assert_not_called()

    def test_record_requires_a_passing_gate_and_current_checkout(self):
        event = {'repository': {'id': self.repo_id, 'full_name': self.repo}, 'pull_request': self.pull}
        env = {'GITHUB_EVENT_NAME': 'pull_request', 'GITHUB_SHA': self.tag,
               'GITHUB_RUN_ID': '80', 'GITHUB_RUN_ATTEMPT': '1'}
        scope = {key: True for key in ci.SCOPES}
        needs = {'changes': {'result': 'success'}, 'checks': {'result': 'success'}}
        proof = release.record(self.root, env, event, scope, needs)
        self.assertEqual(proof['tree'], self.tree)
        self.assertIsNone(release.record(self.root, env, event, ci.plan(['lib/app.dart']), needs))
        self.pull['head']['repo']['id'] = 43
        self.assertIsNone(release.record(self.root, env, event, scope, needs))
        self.pull['head']['repo']['id'] = self.repo_id
        env['GITHUB_SHA'] = self.tested
        with self.assertRaises(ValueError): release.record(self.root, env, event, scope, needs)
        env['GITHUB_SHA'] = self.tag
        needs['checks']['result'] = 'failure'
        with self.assertRaises(ValueError): release.record(self.root, env, event, scope, needs)

    def test_unsupported_archive_encoding_is_not_read(self):
        stream = io.BytesIO()
        with zipfile.ZipFile(stream, 'w', zipfile.ZIP_BZIP2) as archive:
            archive.writestr('verification.json', '{}')
        data = stream.getvalue()
        with self.assertRaises(ValueError):
            release.read_proof(data, {'digest': 'sha256:' + hashlib.sha256(data).hexdigest()})


class PublicationGateTests(unittest.TestCase):
    def needs(self, reuse):
        return {'source': {'result': 'success'},
                'package': {'result': 'success' if reuse else 'skipped', 'outputs': {'cli-artifact-id': '91'}},
                'verify': {'result': 'skipped' if reuse else 'success', 'outputs': {'cli-artifact-id': '92'}}}

    def test_only_selected_successful_branch_supplies_the_artifact(self):
        self.assertEqual(release.candidate(True, self.needs(True)), '91')
        self.assertEqual(release.candidate(False, self.needs(False)), '92')

    def test_all_missing_failed_cancelled_or_unexpected_branches_block_publication(self):
        for reuse in (True, False):
            for job in ('source', 'verify', 'package'):
                for result in ('failure', 'cancelled', 'skipped', 'success', None):
                    needs = self.needs(reuse)
                    if needs[job]['result'] == result: continue
                    needs[job]['result'] = result
                    with self.subTest(reuse=reuse, job=job, result=result), self.assertRaises(ValueError):
                        release.candidate(reuse, needs)
                needs = self.needs(reuse)
                del needs[job]
                with self.assertRaises(ValueError): release.candidate(reuse, needs)

    def test_missing_or_injected_artifact_identity_blocks(self):
        for value in ('', '0', '-1', '1\nother=value', 91):
            needs = self.needs(True)
            needs['package']['outputs']['cli-artifact-id'] = value
            with self.subTest(value=value), self.assertRaises(ValueError): release.candidate(True, needs)

    def test_api_failures_use_full_fallback_and_never_enable_reuse(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory)
            env = {'GITHUB_REPOSITORY': 'owner/repo', 'GITHUB_REPOSITORY_ID': '42',
                   'GITHUB_SHA': 'a' * 40, 'GITHUB_OUTPUT': str(path/'output'),
                   'GITHUB_STEP_SUMMARY': str(path/'summary')}
            for error in (ValueError('invalid proof'), KeyError('missing'), OSError('offline'),
                          subprocess.TimeoutExpired('gh', 10)):
                with self.subTest(error=type(error).__name__), patch.dict(os.environ, env), \
                        patch.object(sys, 'argv', ['release_verification.py', 'select', '--output', str(path/'decision')]), \
                        patch.object(release, 'select', side_effect=error), patch('sys.stdout', new_callable=io.StringIO):
                    release.main()
                    self.assertFalse(json.loads((path/'decision').read_text())['reuse'])
                    self.assertNotIn('reuse=true', (path/'output').read_text())


class WorkflowTests(unittest.TestCase):
    def test_publication_condition_handles_skipped_ancestors_without_bypassing_gates(self):
        # Evaluate the actual job predicate over both release paths and failed
        # gates. This checks its boolean contract, not GitHub's job scheduler.
        workflow = (Path(__file__).resolve().parent.parent /
                    '.github/workflows/release.yml').read_text()
        job = re.split(r'\n  [a-z][\w-]*:\n',
                       workflow.split('\n  public-release:\n', 1)[1])[0]
        self.assertIn('    needs: [source, candidate]\n', job)
        self.assertIn('    environment: public-release\n', job)
        conditions = re.findall(r'^    if: (.+)$', job, re.MULTILINE)
        self.assertEqual(len(conditions), 1)
        expression = conditions[0].removeprefix('${{').removesuffix('}}').strip()
        # A predicate without a status function still has implicit success(),
        # which blocks both paths because one ancestor was deliberately skipped.
        self.assertRegex(expression, r'\b(?:always|cancelled|failure|success)\(\)')
        expression = expression.replace('&&', 'and').replace('||', 'or')
        expression = re.sub(r'!(?!=)', 'not ', expression)
        for reuse in (True, False):
            for cancelled in (False, True):
                for source in ('success', 'failure', 'cancelled', 'skipped', ''):
                    for candidate in ('success', 'failure', 'cancelled', 'skipped', ''):
                        results = {
                            'source': source, 'candidate': candidate,
                            'verify': 'skipped' if reuse else 'success',
                            'package': 'success' if reuse else 'skipped',
                        }
                        context = {
                            'needs': SimpleNamespace(**{
                                key: SimpleNamespace(result=value)
                                for key, value in results.items()}),
                            'always': lambda: True,
                            'cancelled': lambda: cancelled,
                            'success': lambda: all(value == 'success' for value in results.values()),
                            'failure': lambda: 'failure' in results.values(),
                        }
                        with self.subTest(reuse=reuse, cancelled=cancelled,
                                          source=source, candidate=candidate):
                            self.assertEqual(
                                eval(expression, {'__builtins__': {}}, context),
                                not cancelled and source == candidate == 'success')

    def test_publication_protection_and_shared_exact_tag_packaging_remain(self):
        root = Path(__file__).resolve().parent.parent
        workflow = (root/'.github/workflows/release.yml').read_text()
        self.assertIn('needs: [source, candidate]', workflow)
        self.assertIn('needs: [source, verify, package]', workflow)
        self.assertIn('if: always()', workflow)
        self.assertIn("if: needs.source.outputs.reuse != 'true'", workflow)
        self.assertIn("if: needs.source.outputs.reuse == 'true'", workflow)
        self.assertIn('full: true', workflow)
        self.assertIn('environment: public-release', workflow)
        self.assertIn('id-token: write', workflow)
        self.assertIn('git merge-base --is-ancestor "$GITHUB_SHA" "$main_sha"', workflow)
        self.assertIn('test "$tag_sha" = "$GITHUB_SHA"', workflow)
        self.assertIn('artifact-ids: ${{ needs.candidate.outputs.cli-artifact-id }}', workflow)
        for name in ('checks.yml', 'release.yml'):
            self.assertIn('uses: ./.github/workflows/release-package.yml', (root/'.github/workflows'/name).read_text())
        package = (root/'.github/workflows/release-package.yml').read_text()
        for guard in ('make release-check FINAL=1', 'fvm flutter pub publish --dry-run',
                      'diff -r ', 'python3 tool/ci.py artifacts', 'bridra-cli-version.json', 'retention-days: 14'):
            self.assertIn(guard, package)


if __name__ == '__main__':
    unittest.main()
