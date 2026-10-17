#!/bin/sh

set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
smoke_script=${BRIDRA_WATCHDOG_TEST_SCRIPT:-$script_dir/ios_simulator_smoke.sh}
test_root=$(mktemp -d "${TMPDIR:-/tmp}/bridra-ios-watchdog-test.XXXXXX")
fake_bin=$test_root/bin
diagnostics_dir=$test_root/diagnostics
output_file=$test_root/output.log
backend_pid_file=$test_root/backend.pid
backend_terminated_file=$test_root/backend.terminated
flutter_pid_file=$test_root/flutter.pid
flutter_terminated_file=$test_root/flutter.terminated
flutter_child_pid_file=$test_root/flutter-child.pid
flutter_child_terminated_file=$test_root/flutter-child.terminated
xcrun_launch_file=$test_root/xcrun-launched
probe_pid_file=$test_root/probe.pid
probe_child_pid_file=$test_root/probe-child.pid
diagnostic_pid_file=$test_root/diagnostic.pid
diagnostic_child_pid_file=$test_root/diagnostic-child.pid

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
  cleanup_process "$flutter_child_pid_file"
  cleanup_process "$backend_pid_file"
  cleanup_process "$probe_pid_file"
  cleanup_process "$probe_child_pid_file"
  cleanup_process "$diagnostic_pid_file"
  cleanup_process "$diagnostic_child_pid_file"
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
  "simctl launch BRIDRA-TEST-DEVICE com.example.bridra")
    echo launched >"$BRIDRA_TEST_XCRUN_LAUNCH_FILE"
    ;;
  "simctl spawn BRIDRA-TEST-DEVICE log show"*Dart*VM*service*)
    case " $* " in
      *" --start @"*) ;;
      *)
        echo "Runner attach polling must be scoped to the current Flutter launch." >&2
        exit 1
        ;;
    esac
    if [ "${BRIDRA_TEST_QUERY_DELAY:-0}" -gt 0 ]; then
      echo "$$" >"$BRIDRA_TEST_PROBE_PID_FILE"
      trap 'exit 0' TERM INT
      sleep "$BRIDRA_TEST_QUERY_DELAY" &
      echo "$!" >"$BRIDRA_TEST_PROBE_CHILD_PID_FILE"
      wait "$!"
    fi
    if [ "${BRIDRA_TEST_RUNNER_ATTACH:-0}" = "1" ]; then
      echo "flutter: The Dart VM service is listening on http://127.0.0.1:12345/"
    fi
    ;;
  "simctl spawn BRIDRA-TEST-DEVICE log show"*)
    echo "fake Runner diagnostic log"
    if [ "${BRIDRA_TEST_DIAGNOSTIC_DELAY:-0}" -gt 0 ]; then
      echo "$$" >"$BRIDRA_TEST_DIAGNOSTIC_PID_FILE"
      trap 'exit 0' TERM INT
      sleep "$BRIDRA_TEST_DIAGNOSTIC_DELAY" &
      echo "$!" >"$BRIDRA_TEST_DIAGNOSTIC_CHILD_PID_FILE"
      wait "$!"
    fi
    ;;
  *)
    echo "unexpected fake xcrun invocation: $*" >&2
    exit 1
    ;;
esac
EOF

cat >"$fake_bin/wc" <<'EOF'
#!/bin/sh
if [ "${BRIDRA_TEST_SLOW_WC:-0}" = "1" ]; then
  sleep 2
fi
exec "$BRIDRA_TEST_REAL_WC" "$@"
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
xcrun simctl launch BRIDRA-TEST-DEVICE com.example.bridra
"$BRIDRA_TEST_FLUTTER_CHILD" &
echo "fake Flutter test started"
while :; do
  if [ "${BRIDRA_TEST_FLUTTER_PROGRESS:-0}" = "1" ]; then
    echo "fake Flutter progress"
  fi
  sleep 1
done
EOF

cat >"$fake_bin/flutter-child" <<'EOF'
#!/bin/sh
set -eu
echo "$$" >"$BRIDRA_TEST_FLUTTER_CHILD_PID_FILE"
trap 'echo terminated >"$BRIDRA_TEST_FLUTTER_CHILD_TERMINATED_FILE"; exit 0' TERM INT
while :; do
  sleep 1
done
EOF

chmod +x \
  "$fake_bin/uname" \
  "$fake_bin/xcrun" \
  "$fake_bin/wc" \
  "$fake_bin/backend" \
  "$fake_bin/flutter" \
  "$fake_bin/flutter-child"

export BRIDRA_TEST_REAL_WC="$(command -v wc)"

started_at=$(date +%s)
set +e
PATH="$fake_bin:$PATH" \
TMPDIR="$test_root/tmp" \
BRIDRA_SERVER_PATH="$fake_bin/backend" \
BRIDRA_FLUTTER="$fake_bin/flutter" \
BRIDRA_IOS_SIMULATOR_DEVICE=BRIDRA-TEST-DEVICE \
BRIDRA_IOS_SIMULATOR_TIMEOUT_SECONDS=20 \
BRIDRA_IOS_SIMULATOR_NO_PROGRESS_SECONDS=4 \
BRIDRA_IOS_SIMULATOR_ATTACH_TIMEOUT_SECONDS=10 \
BRIDRA_IOS_SIMULATOR_LAUNCH_DELAY_SECONDS=1 \
BRIDRA_IOS_SIMULATOR_WATCHDOG_INTERVAL_SECONDS=1 \
BRIDRA_IOS_SIMULATOR_DIAGNOSTICS_DIR="$diagnostics_dir" \
BRIDRA_TEST_BACKEND_PID_FILE="$backend_pid_file" \
BRIDRA_TEST_BACKEND_TERMINATED_FILE="$backend_terminated_file" \
BRIDRA_TEST_FLUTTER_PID_FILE="$flutter_pid_file" \
BRIDRA_TEST_FLUTTER_TERMINATED_FILE="$flutter_terminated_file" \
BRIDRA_TEST_FLUTTER_CHILD="$fake_bin/flutter-child" \
BRIDRA_TEST_FLUTTER_CHILD_PID_FILE="$flutter_child_pid_file" \
BRIDRA_TEST_FLUTTER_CHILD_TERMINATED_FILE="$flutter_child_terminated_file" \
BRIDRA_TEST_XCRUN_LAUNCH_FILE="$xcrun_launch_file" \
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

grep -Fq "Flutter integration test produced no output for 4s." "$output_file"
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
grep -Fq "reason=Flutter integration test produced no output for 4s." \
  "$diagnostics_dir/summary.txt"
grep -Fq "fake Flutter test started" "$diagnostics_dir/flutter-test.log"
grep -Fq "Delaying iOS Simulator launch by 1s for Flutter log reader readiness." \
  "$diagnostics_dir/flutter-test.log"
grep -Fq "server: listening on 127.0.0.1" "$diagnostics_dir/backend.log"
grep -Fq "fake Runner diagnostic log" "$diagnostics_dir/runner.log"
if [ ! -f "$xcrun_launch_file" ]; then
  echo "Flutter did not delegate Simulator launch through the xcrun wrapper." >&2
  exit 1
fi

if [ ! -f "$flutter_terminated_file" ]; then
  echo "Watchdog did not terminate the fake Flutter process." >&2
  exit 1
fi
if [ ! -f "$flutter_child_terminated_file" ]; then
  if [ -f "$flutter_child_pid_file" ]; then
    child_process_id=$(cat "$flutter_child_pid_file")
    ps -o pid=,ppid=,state=,command= -p "$child_process_id" >&2 || true
  fi
  echo "Watchdog did not terminate the fake Flutter child process." >&2
  exit 1
fi
if [ ! -f "$backend_terminated_file" ]; then
  echo "Watchdog cleanup did not terminate the fake backend process." >&2
  exit 1
fi
for pid_file in \
  "$flutter_pid_file" \
  "$flutter_child_pid_file" \
  "$backend_pid_file"; do
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
  "$flutter_child_terminated_file"

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
BRIDRA_IOS_SIMULATOR_LAUNCH_DELAY_SECONDS=1 \
BRIDRA_IOS_SIMULATOR_WATCHDOG_INTERVAL_SECONDS=1 \
BRIDRA_IOS_SIMULATOR_DIAGNOSTICS_DIR="$diagnostics_dir" \
BRIDRA_TEST_BACKEND_PID_FILE="$backend_pid_file" \
BRIDRA_TEST_BACKEND_TERMINATED_FILE="$backend_terminated_file" \
BRIDRA_TEST_FLUTTER_PID_FILE="$flutter_pid_file" \
BRIDRA_TEST_FLUTTER_TERMINATED_FILE="$flutter_terminated_file" \
BRIDRA_TEST_FLUTTER_CHILD="$fake_bin/flutter-child" \
BRIDRA_TEST_FLUTTER_CHILD_PID_FILE="$flutter_child_pid_file" \
BRIDRA_TEST_FLUTTER_CHILD_TERMINATED_FILE="$flutter_child_terminated_file" \
BRIDRA_TEST_XCRUN_LAUNCH_FILE="$xcrun_launch_file" \
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

for terminated_file in \
  "$flutter_terminated_file" \
  "$flutter_child_terminated_file" \
  "$backend_terminated_file"; do
  if [ ! -f "$terminated_file" ]; then
    echo "Attach watchdog did not terminate process tracked by $terminated_file." >&2
    exit 1
  fi
done

for pid_file in \
  "$flutter_pid_file" \
  "$flutter_child_pid_file" \
  "$backend_pid_file"; do
  process_id=$(cat "$pid_file")
  if kill -0 "$process_id" 2>/dev/null; then
    echo "Attach watchdog left process $process_id running." >&2
    exit 1
  fi
done

run_adversarial_case() {
  case_name=$1
  case_timeout=$2
  case_idle=$3
  expected_reason=$4
  max_elapsed=$5
  rm -rf "$diagnostics_dir"
  if [ "${BRIDRA_TEST_DIAGNOSTICS_AS_FILE:-0}" = "1" ]; then
    : >"$diagnostics_dir"
  fi
  rm -f "$probe_pid_file" "$probe_child_pid_file" "$diagnostic_pid_file" "$diagnostic_child_pid_file"
  started_at=$(date +%s)
  set +e
  PATH="$fake_bin:$PATH" \
  TMPDIR="$test_root/tmp" \
  BRIDRA_SERVER_PATH="$fake_bin/backend" \
  BRIDRA_FLUTTER="$fake_bin/flutter" \
  BRIDRA_IOS_SIMULATOR_DEVICE=BRIDRA-TEST-DEVICE \
  BRIDRA_IOS_SIMULATOR_TIMEOUT_SECONDS="$case_timeout" \
  BRIDRA_IOS_SIMULATOR_NO_PROGRESS_SECONDS="$case_idle" \
  BRIDRA_IOS_SIMULATOR_ATTACH_TIMEOUT_SECONDS=10 \
  BRIDRA_IOS_SIMULATOR_LAUNCH_DELAY_SECONDS=0 \
  BRIDRA_IOS_SIMULATOR_WATCHDOG_INTERVAL_SECONDS=1 \
  BRIDRA_IOS_SIMULATOR_DIAGNOSTICS_TIMEOUT_SECONDS=2 \
  BRIDRA_IOS_SIMULATOR_DIAGNOSTICS_DIR="$diagnostics_dir" \
  BRIDRA_TEST_BACKEND_PID_FILE="$backend_pid_file" \
  BRIDRA_TEST_BACKEND_TERMINATED_FILE="$backend_terminated_file" \
  BRIDRA_TEST_FLUTTER_PID_FILE="$flutter_pid_file" \
  BRIDRA_TEST_FLUTTER_TERMINATED_FILE="$flutter_terminated_file" \
  BRIDRA_TEST_FLUTTER_CHILD="$fake_bin/flutter-child" \
  BRIDRA_TEST_FLUTTER_CHILD_PID_FILE="$flutter_child_pid_file" \
  BRIDRA_TEST_FLUTTER_CHILD_TERMINATED_FILE="$flutter_child_terminated_file" \
  BRIDRA_TEST_XCRUN_LAUNCH_FILE="$xcrun_launch_file" \
  BRIDRA_TEST_PROBE_PID_FILE="$probe_pid_file" \
  BRIDRA_TEST_PROBE_CHILD_PID_FILE="$probe_child_pid_file" \
  BRIDRA_TEST_DIAGNOSTIC_PID_FILE="$diagnostic_pid_file" \
  BRIDRA_TEST_DIAGNOSTIC_CHILD_PID_FILE="$diagnostic_child_pid_file" \
    sh "$smoke_script" >"$output_file" 2>&1
  status=$?
  set -e
  elapsed_seconds=$(($(date +%s) - started_at))
  if [ "$status" -ne 124 ] || [ "$elapsed_seconds" -ge "$max_elapsed" ]; then
    cat "$output_file" >&2
    echo "$case_name: exit=$status elapsed=${elapsed_seconds}s" >&2
    exit 1
  fi
  grep -Fq "$expected_reason" "$output_file"
  for pid_file in "$flutter_pid_file" "$flutter_child_pid_file" "$backend_pid_file" \
    "$probe_pid_file" "$probe_child_pid_file" "$diagnostic_pid_file" "$diagnostic_child_pid_file"; do
    if [ -f "$pid_file" ] && kill -0 "$(cat "$pid_file")" 2>/dev/null; then
      echo "$case_name left an owned process running: $(cat "$pid_file")" >&2
      exit 1
    fi
  done
  echo "$case_name passed in ${elapsed_seconds}s."
}

BRIDRA_TEST_QUERY_DELAY=8 BRIDRA_TEST_FLUTTER_PROGRESS=0 \
  run_adversarial_case slow-probe 20 3 "Flutter integration test produced no output for 3s." 15
if [ ! -f "$probe_pid_file" ] || [ ! -f "$probe_child_pid_file" ]; then
  echo "Slow probe fixture did not exercise the background query." >&2
  exit 1
fi
BRIDRA_TEST_SLOW_WC=1 BRIDRA_TEST_FLUTTER_PROGRESS=1 \
  run_adversarial_case elapsed-work 4 20 "Flutter integration test exceeded 4s." 13
BRIDRA_TEST_DIAGNOSTIC_DELAY=8 BRIDRA_TEST_FLUTTER_PROGRESS=0 \
  run_adversarial_case bounded-diagnostics 20 3 "Flutter integration test produced no output for 3s." 15
grep -Fq "Runner log collection exceeded 2s; partial diagnostics retained." "$diagnostics_dir/runner.log"
grep -Fq "fake Runner diagnostic log" "$diagnostics_dir/runner.log"

BRIDRA_TEST_DIAGNOSTICS_AS_FILE=1 BRIDRA_TEST_FLUTTER_PROGRESS=0 \
  run_adversarial_case unavailable-diagnostics 20 3 "Flutter integration test produced no output for 3s." 15
if [ ! -f "$diagnostics_dir" ]; then
  echo "Unavailable diagnostics fixture was not exercised." >&2
  exit 1
fi

echo "iOS Simulator watchdog regression test passed (6 scenarios)."
