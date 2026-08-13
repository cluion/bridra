#!/bin/sh

set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
smoke_script=$script_dir/ios_simulator_smoke.sh
test_root=$(mktemp -d "${TMPDIR:-/tmp}/bridra-ios-watchdog-test.XXXXXX")
fake_bin=$test_root/bin
diagnostics_dir=$test_root/diagnostics
output_file=$test_root/output.log
backend_pid_file=$test_root/backend.pid
backend_terminated_file=$test_root/backend.terminated
flutter_pid_file=$test_root/flutter.pid
flutter_terminated_file=$test_root/flutter.terminated
runner_log_pid_file=$test_root/runner-log.pid
runner_log_terminated_file=$test_root/runner-log.terminated

cleanup_process() {
  pid_file=$1
  if [ ! -f "$pid_file" ]; then
    return
  fi
  process_id=$(cat "$pid_file")
  case "$process_id" in
    ''|*[!0-9]*) return ;;
  esac
  if kill -0 "$process_id" 2>/dev/null; then
    kill "$process_id" 2>/dev/null || true
  fi
}

cleanup() {
  cleanup_process "$flutter_pid_file"
  cleanup_process "$runner_log_pid_file"
  cleanup_process "$backend_pid_file"
  if [ -n "$test_root" ] && [ -d "$test_root" ]; then
    rm -rf "$test_root"
  fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

mkdir -p "$fake_bin" "$test_root/tmp"

cat >"$fake_bin/uname" <<'EOF'
#!/bin/sh
echo Darwin
EOF

cat >"$fake_bin/xcrun" <<'EOF'
#!/bin/sh
case "$*" in
  "simctl list devices booted"|"simctl list devices available"|"simctl list devices")
    echo "    iPhone Bridra Test (BRIDRA-TEST-DEVICE) (Booted)"
    ;;
  "simctl bootstatus BRIDRA-TEST-DEVICE -b")
    ;;
  "simctl spawn BRIDRA-TEST-DEVICE log show"*)
    echo "fake Runner diagnostic log"
    ;;
  "simctl spawn BRIDRA-TEST-DEVICE log stream"*)
    echo "$$" >"$BRIDRA_TEST_RUNNER_LOG_PID_FILE"
    trap 'echo terminated >"$BRIDRA_TEST_RUNNER_LOG_TERMINATED_FILE"; exit 0' TERM INT
    if [ "${BRIDRA_TEST_RUNNER_ATTACH:-0}" = "1" ]; then
      echo "flutter: The Dart VM service is listening on http://127.0.0.1:12345/"
    fi
    while :; do
      sleep 1
    done
    ;;
  *)
    echo "unexpected fake xcrun invocation: $*" >&2
    exit 1
    ;;
esac
EOF

cat >"$fake_bin/backend" <<'EOF'
#!/bin/sh
set -eu
echo "$$" >"$BRIDRA_TEST_BACKEND_PID_FILE"
trap 'echo terminated >"$BRIDRA_TEST_BACKEND_TERMINATED_FILE"; exit 0' TERM INT
echo "server: listening on 127.0.0.1"
while :; do
  sleep 1
done
EOF

cat >"$fake_bin/flutter" <<'EOF'
#!/bin/sh
set -eu
echo "$$" >"$BRIDRA_TEST_FLUTTER_PID_FILE"
trap 'echo terminated >"$BRIDRA_TEST_FLUTTER_TERMINATED_FILE"; exit 0' TERM INT
echo "fake Flutter test started"
while :; do
  if [ "${BRIDRA_TEST_FLUTTER_PROGRESS:-0}" = "1" ]; then
    echo "fake Flutter progress"
  fi
  sleep 1
done
EOF

chmod +x "$fake_bin/uname" "$fake_bin/xcrun" "$fake_bin/backend" "$fake_bin/flutter"

started_at=$(date +%s)
set +e
PATH="$fake_bin:$PATH" \
TMPDIR="$test_root/tmp" \
BRIDRA_SERVER_PATH="$fake_bin/backend" \
BRIDRA_FLUTTER="$fake_bin/flutter" \
BRIDRA_IOS_SIMULATOR_DEVICE=BRIDRA-TEST-DEVICE \
BRIDRA_IOS_SIMULATOR_TIMEOUT_SECONDS=20 \
BRIDRA_IOS_SIMULATOR_NO_PROGRESS_SECONDS=2 \
BRIDRA_IOS_SIMULATOR_ATTACH_TIMEOUT_SECONDS=10 \
BRIDRA_IOS_SIMULATOR_WATCHDOG_INTERVAL_SECONDS=1 \
BRIDRA_IOS_SIMULATOR_DIAGNOSTICS_DIR="$diagnostics_dir" \
BRIDRA_TEST_BACKEND_PID_FILE="$backend_pid_file" \
BRIDRA_TEST_BACKEND_TERMINATED_FILE="$backend_terminated_file" \
BRIDRA_TEST_FLUTTER_PID_FILE="$flutter_pid_file" \
BRIDRA_TEST_FLUTTER_TERMINATED_FILE="$flutter_terminated_file" \
BRIDRA_TEST_RUNNER_LOG_PID_FILE="$runner_log_pid_file" \
BRIDRA_TEST_RUNNER_LOG_TERMINATED_FILE="$runner_log_terminated_file" \
  "$smoke_script" >"$output_file" 2>&1
status=$?
set -e
elapsed_seconds=$(($(date +%s) - started_at))

if [ "$status" -ne 124 ]; then
  cat "$output_file" >&2
  echo "iOS Simulator watchdog exit status = $status, want 124." >&2
  exit 1
fi
if [ "$elapsed_seconds" -ge 15 ]; then
  cat "$output_file" >&2
  echo "iOS Simulator watchdog took ${elapsed_seconds}s, want less than 15s." >&2
  exit 1
fi

grep -Fq "Flutter integration test produced no output for 2s." "$output_file"
grep -Fq "Saved iOS Simulator diagnostics to $diagnostics_dir" "$output_file"

for diagnostic_file in \
  summary.txt \
  flutter-test.log \
  backend.log \
  devices.txt \
  processes.txt \
  runner.log; do
  if [ ! -f "$diagnostics_dir/$diagnostic_file" ]; then
    echo "Missing iOS Simulator diagnostic: $diagnostic_file" >&2
    exit 1
  fi
done

grep -Fq "exit_status=124" "$diagnostics_dir/summary.txt"
grep -Fq "reason=Flutter integration test produced no output for 2s." \
  "$diagnostics_dir/summary.txt"
grep -Fq "fake Flutter test started" "$diagnostics_dir/flutter-test.log"
grep -Fq "server: listening on 127.0.0.1" "$diagnostics_dir/backend.log"
grep -Fq "fake Runner diagnostic log" "$diagnostics_dir/runner.log"

if [ ! -f "$flutter_terminated_file" ]; then
  echo "Watchdog did not terminate the fake Flutter process." >&2
  exit 1
fi
if [ ! -f "$backend_terminated_file" ]; then
  echo "Watchdog cleanup did not terminate the fake backend process." >&2
  exit 1
fi
if [ ! -f "$runner_log_terminated_file" ]; then
  echo "Watchdog cleanup did not terminate the fake Runner log stream." >&2
  exit 1
fi

for pid_file in "$flutter_pid_file" "$backend_pid_file" "$runner_log_pid_file"; do
  process_id=$(cat "$pid_file")
  if kill -0 "$process_id" 2>/dev/null; then
    echo "Watchdog left process $process_id running." >&2
    exit 1
  fi
done

rm -rf "$diagnostics_dir"
rm -f \
  "$backend_terminated_file" \
  "$flutter_terminated_file" \
  "$runner_log_terminated_file"

started_at=$(date +%s)
set +e
PATH="$fake_bin:$PATH" \
TMPDIR="$test_root/tmp" \
BRIDRA_SERVER_PATH="$fake_bin/backend" \
BRIDRA_FLUTTER="$fake_bin/flutter" \
BRIDRA_IOS_SIMULATOR_DEVICE=BRIDRA-TEST-DEVICE \
BRIDRA_IOS_SIMULATOR_TIMEOUT_SECONDS=20 \
BRIDRA_IOS_SIMULATOR_NO_PROGRESS_SECONDS=10 \
BRIDRA_IOS_SIMULATOR_ATTACH_TIMEOUT_SECONDS=2 \
BRIDRA_IOS_SIMULATOR_WATCHDOG_INTERVAL_SECONDS=1 \
BRIDRA_IOS_SIMULATOR_DIAGNOSTICS_DIR="$diagnostics_dir" \
BRIDRA_TEST_BACKEND_PID_FILE="$backend_pid_file" \
BRIDRA_TEST_BACKEND_TERMINATED_FILE="$backend_terminated_file" \
BRIDRA_TEST_FLUTTER_PID_FILE="$flutter_pid_file" \
BRIDRA_TEST_FLUTTER_TERMINATED_FILE="$flutter_terminated_file" \
BRIDRA_TEST_RUNNER_LOG_PID_FILE="$runner_log_pid_file" \
BRIDRA_TEST_RUNNER_LOG_TERMINATED_FILE="$runner_log_terminated_file" \
BRIDRA_TEST_RUNNER_ATTACH=1 \
BRIDRA_TEST_FLUTTER_PROGRESS=1 \
  "$smoke_script" >"$output_file" 2>&1
status=$?
set -e
elapsed_seconds=$(($(date +%s) - started_at))

if [ "$status" -ne 124 ]; then
  cat "$output_file" >&2
  echo "iOS Simulator attach watchdog exit status = $status, want 124." >&2
  exit 1
fi
if [ "$elapsed_seconds" -ge 15 ]; then
  cat "$output_file" >&2
  echo "iOS Simulator attach watchdog took ${elapsed_seconds}s, want less than 15s." >&2
  exit 1
fi

grep -Fq "Dart VM service detected; waiting for the first smoke RPC." "$output_file"
grep -Fq \
  "Flutter test driver did not attach within 2s after the Dart VM service started." \
  "$output_file"
grep -Fq "attach_timeout_seconds=2" "$diagnostics_dir/summary.txt"
grep -Fq \
  "reason=Flutter test driver did not attach within 2s after the Dart VM service started." \
  "$diagnostics_dir/summary.txt"

echo "iOS Simulator watchdog regression test passed."
