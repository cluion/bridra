#!/bin/sh

set -eu

if [ "$(uname -s)" != "Darwin" ]; then
  echo "iOS Simulator smoke tests must run on macOS." >&2
  exit 1
fi

command -v xcrun >/dev/null 2>&1 || {
  echo "xcrun is required for iOS Simulator smoke tests." >&2
  exit 1
}
command -v pgrep >/dev/null 2>&1 || {
  echo "pgrep is required for iOS Simulator smoke tests." >&2
  exit 1
}
command -v ps >/dev/null 2>&1 || {
  echo "ps is required for iOS Simulator smoke tests." >&2
  exit 1
}

server_path=${BRIDRA_SERVER_PATH:?BRIDRA_SERVER_PATH is required}
flutter_command=${BRIDRA_FLUTTER:-fvm flutter}
device=${BRIDRA_IOS_SIMULATOR_DEVICE:-}
port=${BRIDRA_IOS_SIMULATOR_PORT:-18080}
token=ios-simulator-smoke-token
test_timeout_seconds=${BRIDRA_IOS_SIMULATOR_TIMEOUT_SECONDS:-540}
test_no_progress_seconds=${BRIDRA_IOS_SIMULATOR_NO_PROGRESS_SECONDS:-240}
test_attach_timeout_seconds=${BRIDRA_IOS_SIMULATOR_ATTACH_TIMEOUT_SECONDS:-90}
watchdog_interval_seconds=${BRIDRA_IOS_SIMULATOR_WATCHDOG_INTERVAL_SECONDS:-5}
diagnostics_dir=${BRIDRA_IOS_SIMULATOR_DIAGNOSTICS_DIR:-}

require_positive_seconds() {
  value=$1
  name=$2
  case "$value" in
    ''|*[!0-9]*)
      echo "$name must be a positive integer." >&2
      exit 1
      ;;
  esac
  if [ "$value" -le 0 ]; then
    echo "$name must be a positive integer." >&2
    exit 1
  fi
}

require_positive_seconds "$test_timeout_seconds" BRIDRA_IOS_SIMULATOR_TIMEOUT_SECONDS
require_positive_seconds "$test_no_progress_seconds" BRIDRA_IOS_SIMULATOR_NO_PROGRESS_SECONDS
require_positive_seconds "$test_attach_timeout_seconds" BRIDRA_IOS_SIMULATOR_ATTACH_TIMEOUT_SECONDS
require_positive_seconds "$watchdog_interval_seconds" BRIDRA_IOS_SIMULATOR_WATCHDOG_INTERVAL_SECONDS

case "$port" in
  ''|*[!0-9]*)
    echo "BRIDRA_IOS_SIMULATOR_PORT must be numeric." >&2
    exit 1
    ;;
esac

if [ ! -x "$server_path" ]; then
  echo "Go HTTP backend is not executable: $server_path" >&2
  exit 1
fi

if [ -z "$device" ]; then
  device=$(xcrun simctl list devices booted | awk -F '[()]' '
    /^[[:space:]]+iPhone/ && $4 ~ /Booted/ { print $2; exit }
  ')
fi
if [ -z "$device" ]; then
  device=$(xcrun simctl list devices available | awk -F '[()]' '
    /^[[:space:]]+iPhone/ && $4 ~ /Shutdown|Booted/ { print $2; exit }
  ')
fi
if [ -z "$device" ]; then
  echo "No available iPhone Simulator was found." >&2
  exit 1
fi

if ! xcrun simctl list devices available | grep -Fq "($device)"; then
  echo "iPhone Simulator is unavailable: $device" >&2
  exit 1
fi

booted_by_script=0
server_pid=
test_pid=
watchdog_reason=
smoke_log=${TMPDIR:-/tmp}/bridra-ios-simulator-smoke.$$.log
flutter_log=${TMPDIR:-/tmp}/bridra-ios-simulator-flutter.$$.log
runner_attach_log=${TMPDIR:-/tmp}/bridra-ios-simulator-attach.$$.log

terminate_process() {
  process_id=$1
  if ! kill -0 "$process_id" 2>/dev/null; then
    wait "$process_id" 2>/dev/null || true
    return
  fi
  kill "$process_id" 2>/dev/null || true
  terminate_attempt=0
  while kill -0 "$process_id" 2>/dev/null && [ "$terminate_attempt" -lt 10 ]; do
    terminate_attempt=$((terminate_attempt + 1))
    sleep 1
  done
  if kill -0 "$process_id" 2>/dev/null; then
    kill -9 "$process_id" 2>/dev/null || true
  fi
  wait "$process_id" 2>/dev/null || true
}

collect_process_tree() {
  tree_parent_process=$1
  if ! kill -0 "$tree_parent_process" 2>/dev/null; then
    return
  fi
  kill -STOP "$tree_parent_process" 2>/dev/null || true
  bridra_process_tree="$bridra_process_tree $tree_parent_process"
  child_processes=$(pgrep -P "$tree_parent_process" 2>/dev/null || true)
  for child_process in $child_processes; do
    collect_process_tree "$child_process"
  done
}

process_is_running() {
  checked_process=$1
  if ! kill -0 "$checked_process" 2>/dev/null; then
    return 1
  fi
  process_state=$(ps -o state= -p "$checked_process" 2>/dev/null || true)
  case "$process_state" in
    ''|*Z*) return 1 ;;
    *) return 0 ;;
  esac
}

terminate_process_tree() {
  tree_root_process=$1
  bridra_process_tree=
  collect_process_tree "$tree_root_process"
  if [ -z "$bridra_process_tree" ]; then
    wait "$tree_root_process" 2>/dev/null || true
    return
  fi
  for tree_process in $bridra_process_tree; do
    kill "$tree_process" 2>/dev/null || true
  done
  for tree_process in $bridra_process_tree; do
    kill -CONT "$tree_process" 2>/dev/null || true
  done
  terminate_attempt=0
  while [ "$terminate_attempt" -lt 10 ]; do
    tree_running=0
    for tree_process in $bridra_process_tree; do
      if process_is_running "$tree_process"; then
        tree_running=1
        break
      fi
    done
    if [ "$tree_running" -eq 0 ]; then
      break
    fi
    terminate_attempt=$((terminate_attempt + 1))
    sleep 1
  done
  for tree_process in $bridra_process_tree; do
    if process_is_running "$tree_process"; then
      kill -9 "$tree_process" 2>/dev/null || true
    fi
  done
  wait "$tree_root_process" 2>/dev/null || true
}

collect_diagnostics() {
  failure_status=$1
  if [ -z "$diagnostics_dir" ]; then
    return
  fi
  mkdir -p "$diagnostics_dir" || return
  {
    echo "recorded_at=$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
    echo "exit_status=$failure_status"
    echo "reason=${watchdog_reason:-command_failed}"
    echo "device=$device"
    echo "timeout_seconds=$test_timeout_seconds"
    echo "no_progress_seconds=$test_no_progress_seconds"
    echo "attach_timeout_seconds=$test_attach_timeout_seconds"
  } >"$diagnostics_dir/summary.txt" 2>&1 || true
  if [ -f "$flutter_log" ]; then
    cp "$flutter_log" "$diagnostics_dir/flutter-test.log" 2>/dev/null || true
  fi
  if [ -f "$smoke_log" ]; then
    cp "$smoke_log" "$diagnostics_dir/backend.log" 2>/dev/null || true
  fi
  xcrun simctl list devices >"$diagnostics_dir/devices.txt" 2>&1 || true
  ps -axo pid=,ppid=,state=,etime=,comm= \
    >"$diagnostics_dir/processes.txt" 2>&1 || true
  xcrun simctl spawn "$device" log show \
    --style compact \
    --last 10m \
    --predicate 'process == "Runner"' \
    >"$diagnostics_dir/runner.log" 2>&1 || true
  echo "Saved iOS Simulator diagnostics to $diagnostics_dir" >&2
}

cleanup() {
  status=$?
  trap - EXIT INT TERM
  if [ -n "$test_pid" ]; then
    terminate_process_tree "$test_pid"
  fi
  if [ "$status" -ne 0 ]; then
    collect_diagnostics "$status"
  fi
  if [ -n "$server_pid" ] && kill -0 "$server_pid" 2>/dev/null; then
    terminate_process "$server_pid"
  fi
  if [ "$booted_by_script" -eq 1 ]; then
    xcrun simctl shutdown "$device" >/dev/null 2>&1 || true
  fi
  rm -f "$smoke_log" "$flutter_log" "$runner_attach_log"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

if ! xcrun simctl list devices booted | grep -Fq "($device) (Booted)"; then
  echo "Booting iPhone Simulator $device..."
  xcrun simctl boot "$device"
  booted_by_script=1
fi
xcrun simctl bootstatus "$device" -b

echo "Starting Go HTTP backend on 127.0.0.1:$port..."
"$server_path" \
  --listen "127.0.0.1:$port" \
  --token "$token" \
  --smoke-stream \
  --smoke-download \
  --smoke-download-resume \
  --smoke-upload-resume \
  --cors-origin '*' >"$smoke_log" 2>&1 &
server_pid=$!

attempt=0
while ! grep -Fq 'server: listening on ' "$smoke_log"; do
  if ! kill -0 "$server_pid" 2>/dev/null; then
    cat "$smoke_log" >&2
    wait "$server_pid" || true
    echo "Go HTTP backend stopped before becoming ready." >&2
    exit 1
  fi
  attempt=$((attempt + 1))
  if [ "$attempt" -ge 100 ]; then
    cat "$smoke_log" >&2
    echo "Go HTTP backend did not become ready." >&2
    exit 1
  fi
  sleep 0.1
done

echo "Running iOS Simulator RPC, streaming, managed-download, and resumable-upload integration test on $device..."
runner_attach_started_at=@$(date +%s)
test_status=0
# BRIDRA_FLUTTER intentionally contains a command and optional wrapper argument.
# shellcheck disable=SC2086
$flutter_command test integration_test/http_smoke_test.dart \
  -d "$device" \
  --dart-define="BRIDRA_BACKEND_URL=http://127.0.0.1:$port/rpc" \
  --dart-define="BRIDRA_BACKEND_TOKEN=$token" \
  --dart-define="BRIDRA_SMOKE_STREAM=true" \
  --dart-define="BRIDRA_SMOKE_DOWNLOAD=true" \
  --dart-define="BRIDRA_SMOKE_UPLOAD_RESUME=true" \
  --dart-define="BRIDRA_SMOKE_CLIENT=iOS Simulator" \
  >"$flutter_log" 2>&1 &
test_pid=$!

elapsed_seconds=0
idle_seconds=0
heartbeat_seconds=0
last_log_size=0
attach_elapsed_seconds=0
attach_detected=0
while kill -0 "$test_pid" 2>/dev/null; do
  sleep "$watchdog_interval_seconds"
  if ! kill -0 "$test_pid" 2>/dev/null; then
    break
  fi
  current_log_size=$(wc -c <"$flutter_log" | tr -d '[:space:]')
  elapsed_seconds=$((elapsed_seconds + watchdog_interval_seconds))
  heartbeat_seconds=$((heartbeat_seconds + watchdog_interval_seconds))
  if [ "$current_log_size" -ne "$last_log_size" ]; then
    last_log_size=$current_log_size
    idle_seconds=0
  else
    idle_seconds=$((idle_seconds + watchdog_interval_seconds))
  fi
  if [ "$attach_detected" -eq 0 ]; then
    xcrun simctl spawn "$device" log show \
      --style compact \
      --start "$runner_attach_started_at" \
      --predicate 'process == "Runner" AND eventMessage CONTAINS "The Dart VM service is listening"' \
      >"$runner_attach_log" 2>&1 || true
    if grep -Fq 'The Dart VM service is listening' "$runner_attach_log"; then
      attach_detected=1
      attach_elapsed_seconds=0
      echo "Dart VM service detected; waiting for the first smoke RPC."
    fi
  elif [ "$attach_detected" -eq 1 ]; then
    if grep -Fq '"rpc_method":"system.health"' "$smoke_log"; then
      attach_detected=2
    else
      attach_elapsed_seconds=$((attach_elapsed_seconds + watchdog_interval_seconds))
      if [ "$attach_elapsed_seconds" -ge "$test_attach_timeout_seconds" ]; then
        watchdog_reason="Flutter test driver did not attach within ${test_attach_timeout_seconds}s after the Dart VM service started."
        test_status=124
        break
      fi
    fi
  fi
  if [ "$elapsed_seconds" -ge "$test_timeout_seconds" ]; then
    watchdog_reason="Flutter integration test exceeded ${test_timeout_seconds}s."
    test_status=124
    break
  fi
  if [ "$idle_seconds" -ge "$test_no_progress_seconds" ]; then
    watchdog_reason="Flutter integration test produced no output for ${test_no_progress_seconds}s."
    test_status=124
    break
  fi
  if [ "$heartbeat_seconds" -ge 30 ]; then
    echo "iOS Simulator smoke still running: ${elapsed_seconds}s elapsed, ${idle_seconds}s without output."
    heartbeat_seconds=0
  fi
done

if [ "$test_status" -eq 0 ]; then
  wait "$test_pid" || test_status=$?
else
  echo "$watchdog_reason" >&2
  terminate_process_tree "$test_pid"
fi
test_pid=

cat "$flutter_log"
cat "$smoke_log"
if [ "$test_status" -ne 0 ]; then
  exit "$test_status"
fi
grep -Fq '"rpc_method":"system.health"' "$smoke_log"
grep -Fq '"rpc_method":"greeting.hello"' "$smoke_log"
grep -Fq '"rpc_method":"bridra.smoke.stream"' "$smoke_log"
grep -Fq '"rpc_method":"bridra.smoke.download"' "$smoke_log"
grep -Fq '"surface":"file_transfer"' "$smoke_log"
grep -Fq 'server: smoke download interrupted at offset 32768' "$smoke_log"
grep -Fq 'server: smoke download resumed at offset 32768' "$smoke_log"
grep -Fq 'server: smoke upload interrupted at offset 32768' "$smoke_log"
grep -Fq 'server: smoke upload resumed at offset 32768' "$smoke_log"
grep -Fq '"rpc_method":"bridra.smoke.upload.verify"' "$smoke_log"
