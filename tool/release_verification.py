"""Reuse complete PR verification only when it proves the tagged source tree."""

import argparse
from datetime import datetime, timezone
import hashlib
import io
import json
import os
from pathlib import Path
import re
import subprocess
import time
import zipfile

import ci


WORKFLOW = ".github/workflows/verify.yml"
REQUIRED_JOBS = ("changes", "checks / core", "checks / linux", "checks / android-web",
                 "checks / android-emulator", "checks / macos", "checks / ios",
                 "checks / windows", "checks / check-results", "ci-gate")
MAX_AGE_SECONDS = 7 * 24 * 60 * 60
MAX_PROOF_BYTES = 64 * 1024


def require(condition, message):
    if not condition:
        raise ValueError(message)


def positive_id(value):
    require(type(value) is int and value > 0, "invalid GitHub identity")
    return value


def sha(value):
    require(isinstance(value, str) and re.fullmatch(r"[0-9a-f]{40}", value),
            "invalid immutable Git SHA")
    return value


def artifact_name(run_id, attempt):
    return f"pr-verification-{positive_id(run_id)}-{positive_id(attempt)}"


def record(root, environment, event, scope, needs):
    ci.check_gate(scope, needs)
    if environment["GITHUB_EVENT_NAME"] != "pull_request" or not all(scope.values()):
        return None
    pull = event["pull_request"]
    repository = event["repository"]
    require(pull["base"]["ref"] == "main", "PR does not target main")
    if (pull["head"].get("repo") or {}).get("id") != repository["id"]:
        return None
    tested = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip()
    require(tested == environment["GITHUB_SHA"], "checkout does not match the tested merge")
    subprocess.run(["git", "diff-index", "--quiet", "HEAD", "--"], cwd=root, check=True)
    tree = subprocess.check_output(["git", "rev-parse", "HEAD^{tree}"], cwd=root, text=True).strip()
    return {
        "schema": 1, "repository_id": positive_id(repository["id"]),
        "repository": repository["full_name"], "workflow": WORKFLOW,
        "run_id": positive_id(int(environment["GITHUB_RUN_ID"])),
        "run_attempt": positive_id(int(environment["GITHUB_RUN_ATTEMPT"])),
        "event": "pull_request", "pull_request": positive_id(pull["number"]),
        "base_sha": sha(pull["base"]["sha"]), "head_sha": sha(pull["head"]["sha"]),
        "tested_sha": sha(tested), "tree": sha(tree), "plan": scope,
    }


class GitHub:
    """A bounded, read-only lookup; uncertainty falls back to normal verification."""

    def __init__(self, repository):
        require(re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository),
                "invalid repository")
        self.prefix = f"repos/{repository}/"
        self.deadline = time.monotonic() + 45

    def read(self, endpoint, binary=False):
        remaining = self.deadline - time.monotonic()
        require(remaining > 0, "evidence lookup budget exceeded")
        data = subprocess.check_output(
            ["gh", "api", "--hostname", "github.com", "--method", "GET",
             "-H", "X-GitHub-Api-Version: 2022-11-28", self.prefix + endpoint],
            timeout=min(10, remaining), stderr=subprocess.PIPE)
        return data if binary else json.loads(data)


def complete_page(response, key):
    values = response[key]
    require(isinstance(values, list) and response["total_count"] == len(values),
            "incomplete GitHub result page")
    return values


def latest_run(api, endpoint):
    runs = complete_page(api.read(endpoint), "workflow_runs")
    require(bool(runs), "no PR verification run")
    return max(runs, key=lambda run: (
        datetime.fromisoformat(run["created_at"].replace("Z", "+00:00")), positive_id(run["id"])))


def read_proof(data, artifact):
    require(len(data) <= MAX_PROOF_BYTES * 2, "verification archive is too large")
    require(artifact.get("digest") == "sha256:" + hashlib.sha256(data).hexdigest(),
            "verification artifact digest mismatch")
    with zipfile.ZipFile(io.BytesIO(data)) as archive:
        require(archive.namelist() == ["verification.json"], "unexpected verification archive files")
        entry = archive.getinfo("verification.json")
        require(entry.file_size <= MAX_PROOF_BYTES and not entry.is_dir(),
                "verification record is too large")
        require(not entry.flag_bits & 1 and entry.compress_type in (zipfile.ZIP_STORED, zipfile.ZIP_DEFLATED),
                "unsupported verification archive encoding")
        return json.loads(archive.read(entry))


def select(root, repository, repository_id, release_sha, api, now=None):
    """Return evidence for one exact merged PR; never search past a newer failure."""
    release_sha = sha(release_sha)
    checked_out = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip()
    require(checked_out == release_sha, "checkout does not match the tag")
    subprocess.run(["git", "diff-index", "--quiet", release_sha, "--"], cwd=root, check=True)
    release_tree = subprocess.check_output(
        ["git", "rev-parse", f"{release_sha}^{{tree}}"], cwd=root, text=True).strip()
    pulls = api.read(f"commits/{release_sha}/pulls?per_page=100")
    matches = [p for p in pulls if p.get("merge_commit_sha") == release_sha
               and p.get("base", {}).get("ref") == "main"]
    require(len(matches) == 1 and len(pulls) < 100, "no unique merged PR for the tag")
    pull = api.read(f"pulls/{positive_id(matches[0]['number'])}")
    require(pull["merged"] is True and pull["state"] == "closed"
            and pull["merge_commit_sha"] == release_sha and pull["base"]["ref"] == "main"
            and pull["base"]["repo"]["id"] == repository_id
            and pull["head"]["repo"]["id"] == repository_id,
            "tag is not the same-repository PR merge into main")
    head = sha(pull["head"]["sha"])
    workflow = api.read("actions/workflows/verify.yml")
    require(workflow["path"] == WORKFLOW and workflow["state"] == "active",
            "verification workflow is not active")
    runs_path = (f"actions/workflows/{positive_id(workflow['id'])}/runs"
                 f"?event=pull_request&head_sha={head}&per_page=100")
    selected_run = latest_run(api, runs_path)
    run = api.read(f"actions/runs/{positive_id(selected_run['id'])}")
    run_id, attempt = positive_id(run["id"]), positive_id(run["run_attempt"])
    require(run["status"] == "completed" and run["conclusion"] == "success"
            and run["event"] == "pull_request" and run["head_sha"] == head
            and run["workflow_id"] == workflow["id"]
            and run["path"].split("@", 1)[0] == WORKFLOW
            and run["repository"]["id"] == repository_id
            and run["repository"]["full_name"] == repository
            and run["head_repository"]["id"] == repository_id,
            "latest PR verification is not a successful trusted run")
    created = datetime.fromisoformat(run["created_at"].replace("Z", "+00:00"))
    age = ((now or datetime.now(timezone.utc)) - created).total_seconds()
    require(0 <= age <= MAX_AGE_SECONDS, "PR verification is expired or future-dated")
    jobs = complete_page(api.read(f"actions/runs/{run_id}/attempts/{attempt}/jobs?per_page=100"), "jobs")
    for name in REQUIRED_JOBS:
        matching = [job for job in jobs if job["name"] == name]
        require(len(matching) == 1 and matching[0]["status"] == "completed"
                and matching[0]["conclusion"] == "success"
                and matching[0]["run_id"] == run_id
                and matching[0]["head_sha"] == head,
                "missing, ambiguous or unsuccessful full verification job")
    artifacts = complete_page(api.read(f"actions/runs/{run_id}/artifacts?per_page=100"), "artifacts")
    matches = [a for a in artifacts if a["name"] == artifact_name(run_id, attempt)]
    require(len(matches) == 1, "missing or ambiguous verification artifact")
    artifact = matches[0]
    require(artifact["expired"] is False and 0 < artifact["size_in_bytes"] <= MAX_PROOF_BYTES * 2
            and artifact["workflow_run"]["id"] == run_id
            and artifact["workflow_run"]["head_sha"] == head,
            "expired or unrelated verification artifact")
    proof = read_proof(api.read(f"actions/artifacts/{positive_id(artifact['id'])}/zip", binary=True), artifact)
    require(proof["schema"] == 1 and proof["repository_id"] == repository_id
            and proof["repository"] == repository and proof["workflow"] == WORKFLOW
            and proof["event"] == "pull_request" and proof["pull_request"] == pull["number"]
            and proof["run_id"] == run_id and proof["run_attempt"] == attempt
            and proof["head_sha"] == head and proof["tree"] == release_tree,
            "verification record does not identify this source and run")
    ci.validate_plan(proof["plan"])
    require(all(proof["plan"].values()), "PR verification did not cover every target")
    # The step name is evaluated by GitHub, independently of the artifact producer.
    # It binds the record to the run's real merge SHA, not merely its PR head SHA.
    gate = next(job for job in jobs if job["name"] == "ci-gate")
    steps = [step for step in gate["steps"]
             if step["name"] == f"Record complete PR verification evidence for {sha(proof['tested_sha'])}"]
    require(len(steps) == 1 and steps[0]["status"] == "completed"
            and steps[0]["conclusion"] == "success", "record does not match the run's tested merge SHA")
    tested = api.read(f"git/commits/{sha(proof['tested_sha'])}")
    require(tested["sha"] == proof["tested_sha"] and tested["tree"]["sha"] == release_tree
            and [p["sha"] for p in tested["parents"]] == [sha(proof["base_sha"]), head],
            "tested merge ancestry or complete source tree differs")
    # A rerun or deletion during selection must not leave an old green decision.
    require(latest_run(api, runs_path)["id"] == run_id, "a newer verification started during lookup")
    latest = api.read(f"actions/runs/{run_id}")
    require(latest["run_attempt"] == attempt and latest["status"] == "completed"
            and latest["conclusion"] == "success", "verification changed during lookup")
    return {"reuse": True, "reason": "complete PR verification matches the tagged source tree",
            "run_id": run_id, "run_attempt": attempt, "pull_request": pull["number"],
            "tested_sha": proof["tested_sha"], "tree": release_tree, "release_sha": release_sha,
            "artifact_id": artifact["id"]}


def candidate(reuse, needs):
    require(set(needs) == {"source", "verify", "package"}, "missing release branch result")
    require(needs["source"]["result"] == "success", "release source check failed")
    selected, skipped = ("package", "verify") if reuse else ("verify", "package")
    require(needs[selected]["result"] == "success" and needs[skipped]["result"] == "skipped",
            "required release verification or packaging did not succeed")
    value = needs[selected].get("outputs", {}).get("cli-artifact-id", "")
    require(isinstance(value, str) and re.fullmatch(r"[1-9][0-9]*", value),
            "missing immutable release artifact ID")
    return value


def output(values):
    with open(os.environ["GITHUB_OUTPUT"], "a") as handle:
        for key, value in values.items():
            handle.write(f"{key}={value}\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    for name in ("record", "select"):
        command = commands.add_parser(name)
        command.add_argument("--root", default=".")
        command.add_argument("--output", required=True)
    commands.add_parser("candidate")
    args = parser.parse_args()
    if args.command == "record":
        proof = record(args.root, os.environ, json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text()),
                       json.loads(os.environ["CI_PLAN"]), json.loads(os.environ["CI_NEEDS"]))
        if proof:
            Path(args.output).write_text(json.dumps(proof, indent=2) + "\n")
        output({"eligible": str(proof is not None).lower()})
    elif args.command == "select":
        try:
            proof = select(args.root, os.environ["GITHUB_REPOSITORY"], int(os.environ["GITHUB_REPOSITORY_ID"]),
                           os.environ["GITHUB_SHA"], GitHub(os.environ["GITHUB_REPOSITORY"]))
        except (ValueError, KeyError, TypeError, OSError, subprocess.SubprocessError, zipfile.BadZipFile):
            proof = {"reuse": False, "reason": "no complete, current and matching trusted PR evidence"}
        Path(args.output).write_text(json.dumps(proof, indent=2) + "\n")
        output({"reuse": str(proof["reuse"]).lower()})
        with open(os.environ["GITHUB_STEP_SUMMARY"], "a") as handle:
            handle.write(f"### Release verification\n\n{proof['reason']}.\n")
            if proof["reuse"]:
                handle.write(f"PR #{proof['pull_request']}, Verify run {proof['run_id']}, "
                             f"attempt {proof['run_attempt']}; tree `{proof['tree']}`.\n")
        print(json.dumps(proof))
    else:
        require(os.environ["REUSE_VERIFICATION"] in ("true", "false"), "invalid release verification mode")
        value = candidate(os.environ["REUSE_VERIFICATION"] == "true", json.loads(os.environ["CI_NEEDS"]))
        output({"cli-artifact-id": value})


if __name__ == "__main__":
    main()
