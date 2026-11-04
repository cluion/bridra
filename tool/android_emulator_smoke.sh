#!/bin/sh

set -eu

command -v adb >/dev/null 2>&1 || {
  echo "adb is required for Android Emulator smoke tests." >&2
  exit 1
}

server_path=${BRIDRA_SERVER_PATH:?BRIDRA_SERVER_PATH is required}
flutter_command=${BRIDRA_FLUTTER:-fvm flutter}
requested_device=${BRIDRA_ANDROID_EMULATOR_DEVICE:-}
port=${BRIDRA_ANDROID_EMULATOR_PORT:-18082}
token=android-emulator-smoke-token

case "$port" in
  ''|*[!0-9]*)
    echo "BRIDRA_ANDROID_EMULATOR_PORT must be numeric." >&2
    exit 1
    ;;
esac
if [ "$port" -lt 1 ] || [ "$port" -gt 65535 ]; then
  echo "BRIDRA_ANDROID_EMULATOR_PORT must be between 1 and 65535." >&2
  exit 1
fi
if [ ! -x "$server_path" ]; then
  echo "Go HTTP backend is not executable: $server_path" >&2
  exit 1
fi

temporary_directory=$(mktemp -d "${TMPDIR:-/tmp}/bridra-android-emulator-smoke.XXXXXX")
devices_file=$temporary_directory/devices.txt
smoke_log=$temporary_directory/backend.log
test_log=$temporary_directory/flutter-test.log
milestone_log=$temporary_directory/milestones.log
diagnostics_dir=${BRIDRA_ANDROID_EMULATOR_DIAGNOSTICS_DIR:-}
diagnostics_collected=0
diagnostic_log_bytes=2097152
started_at=$(date +%s)
recorded_stages=' '
failure_reason=command_failed
server_pid=
test_pid=
: >"$smoke_log"
: >"$test_log"
: >"$milestone_log"

redact_output() {
  sed -E \
    -e "s/$token/[REDACTED]/g" \
    -e 's/(BRIDRA_BACKEND_TOKEN=)[^[:space:]"]+/\1[REDACTED]/g' \
    -e 's/([Aa]uthorization:[[:space:]]*[Bb]earer[[:space:]]+)[^[:space:]"]+/\1[REDACTED]/g'
}

record_stage() {
  stage=$1
  case "$recorded_stages" in *" $stage "*) return ;; esac
  recorded_stages="$recorded_stages$stage "
  progress_line="stage=$stage observed_at=$(date -u '+%Y-%m-%dT%H:%M:%SZ') elapsed_seconds=$(($(date +%s) - started_at))"
  printf '%s\n' "$progress_line" >>"$milestone_log"
  printf '[android-smoke] %s\n' "$progress_line"
}

record_progress() {
  observed_stages=$(awk '
    function observed(stage) { if (!seen[stage]++) print stage }
    /Running Gradle task/ { observed("apk_build_started") }
    /Built build\// { observed("apk_built") }
    /Installing build\// { observed("apk_install_observed") }
    /shell am start/ { observed("activity_launch_requested") }
    /Waiting for VM Service port/ { observed("vm_service_discovery_started") }
    /VM Service URL on device:|Connecting to vm service|Connecting to VM Service at/ {
      observed("vm_service_discovered")
    }
    /Finding the correct isolate/ { observed("test_isolate_discovery_started") }
    /"rpc_method":"system.health"/ { observed("first_health_completed") }
    /"rpc_method":"bridra.smoke.stream"/ { observed("first_stream_completed") }
  ' "$test_log" "$smoke_log")
  for observed_stage in $observed_stages; do
    record_stage "$observed_stage"
  done
}

save_diagnostic_logs() {
  for log in flutter-test backend milestones; do
    case "$log" in
      flutter-test) source_log=$test_log ;;
      backend) source_log=$smoke_log ;;
      milestones) source_log=$milestone_log ;;
    esac
    redact_output <"$source_log" | tail -c "$diagnostic_log_bytes" \
      >"$diagnostics_dir/$log.log"
  done
  {
    echo "recorded_at=$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
    echo "exit_status=$1"
    echo "reason=$failure_reason"
    echo "device=${device:-unselected}"
    echo 'diagnostic_command_timeout_seconds=3'
    echo 'diagnostic_total_timeout_seconds=30'
    echo "text_log_limit_bytes=$diagnostic_log_bytes"
  } | redact_output >"$diagnostics_dir/summary.txt"
}

diagnostic_command() {
  diagnostic_name=$1
  diagnostic_kind=$2
  shift 2
  if [ "$(($(date +%s) - diagnostic_started_at))" -ge 30 ]; then
    echo "$diagnostic_name skipped: diagnostic time budget exhausted" \
      >>"$diagnostics_dir/commands.txt"
    return
  fi
  diagnostic_raw=$temporary_directory/diagnostic.raw
  "$@" >"$diagnostic_raw" 2>&1 &
  diagnostic_pid=$!
  diagnostic_attempt=0
  diagnostic_timed_out=false
  while kill -0 "$diagnostic_pid" 2>/dev/null; do
    if [ "$diagnostic_attempt" -ge 30 ] ||
      [ "$(($(date +%s) - diagnostic_started_at))" -ge 30 ]; then
      diagnostic_timed_out=true
      kill -9 "$diagnostic_pid" 2>/dev/null || true
      break
    fi
    diagnostic_attempt=$((diagnostic_attempt + 1))
    sleep 0.1
  done
  diagnostic_status=0
  wait "$diagnostic_pid" 2>/dev/null || diagnostic_status=$?
  echo "$diagnostic_name exit_status=$diagnostic_status timed_out=$diagnostic_timed_out" \
    >>"$diagnostics_dir/commands.txt"
  if [ "$diagnostic_kind" = png ] && [ "$diagnostic_status" -eq 0 ] &&
    [ "$(od -An -tx1 -N8 "$diagnostic_raw" | tr -d ' \n')" = 89504e470d0a1a0a ]; then
    cp "$diagnostic_raw" "$diagnostics_dir/$diagnostic_name.png"
  else
    redact_output <"$diagnostic_raw" | tail -c "$diagnostic_log_bytes" \
      >"$diagnostics_dir/$diagnostic_name.txt"
  fi
  rm -f "$diagnostic_raw"
}

collect_diagnostics() {
  if [ -z "$diagnostics_dir" ]; then return; fi
  if ! mkdir -p "$diagnostics_dir"; then
    echo 'Unable to create Android smoke diagnostics directory.' >&2
    return 1
  fi
  record_progress
  save_diagnostic_logs "$1"
  if [ "$diagnostics_collected" -eq 1 ]; then return; fi
  diagnostics_collected=1
  diagnostic_started_at=$(date +%s)
  diagnostic_command devices text adb devices -l
  diagnostic_command host-processes text ps -axo pid=,ppid=,state=,etime=,comm=
  if [ -n "${device:-}" ]; then
    diagnostic_command device-state text adb -s "$device" get-state
    diagnostic_command logcat text adb -s "$device" logcat \
      -b main -b system -b crash -d -t 2000 -v threadtime
    diagnostic_command activities text adb -s "$device" shell dumpsys activity activities
    diagnostic_command app-processes text adb -s "$device" shell ps -A
    diagnostic_command forwards text adb -s "$device" forward --list
    diagnostic_command screenshot png adb -s "$device" exec-out screencap -p
  fi
  echo "Saved Android Emulator diagnostics to $diagnostics_dir" >&2
}

cleanup() {
  status=$?
  trap - EXIT INT TERM
  if [ "$status" -ne 0 ]; then
    collect_diagnostics "$status" || true
  fi
  if [ -n "$test_pid" ] && kill -0 "$test_pid" 2>/dev/null; then
    kill "$test_pid" 2>/dev/null || true
    wait "$test_pid" 2>/dev/null || true
  fi
  if [ -n "$server_pid" ] && kill -0 "$server_pid" 2>/dev/null; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  rm -f "$devices_file" "$smoke_log" "$test_log" "$milestone_log"
  rmdir "$temporary_directory" 2>/dev/null || true
  exit "$status"
}
trap cleanup EXIT
trap 'failure_reason=interrupted; exit 130' INT
trap 'failure_reason=terminated; exit 143' TERM

record_stage script_started

adb devices >"$devices_file"
device=
available_devices=0
while read -r identifier state _; do
  case "$identifier" in
    emulator-*) ;;
    *) continue ;;
  esac
  if [ "$state" != "device" ]; then
    continue
  fi
  if [ -n "$requested_device" ] && [ "$identifier" = "$requested_device" ]; then
    device=$identifier
  elif [ -z "$requested_device" ]; then
    available_devices=$((available_devices + 1))
    if [ "$available_devices" -eq 1 ]; then
      device=$identifier
    fi
  fi
done <"$devices_file"

if [ -n "$requested_device" ] && [ -z "$device" ]; then
  echo "Android Emulator is unavailable: $requested_device" >&2
  exit 1
fi
if [ -z "$requested_device" ] && [ "$available_devices" -eq 0 ]; then
  echo "No running Android Emulator was found." >&2
  exit 1
fi
if [ -z "$requested_device" ] && [ "$available_devices" -gt 1 ]; then
  echo "Multiple Android Emulators are running; set DEVICE=<emulator-id>." >&2
  exit 1
fi

echo "Waiting for Android Emulator $device..."
adb -s "$device" wait-for-device
attempt=0
while [ "$(adb -s "$device" shell getprop sys.boot_completed 2>/dev/null | tr -d '\r')" != "1" ]; do
  attempt=$((attempt + 1))
  if [ "$attempt" -ge 120 ]; then
    echo "Android Emulator did not finish booting within 120 seconds." >&2
    exit 1
  fi
  sleep 1
done
record_stage emulator_ready

backend_log_count() {
  grep -c "$1" "$smoke_log" || true
}

health_count() {
  backend_log_count '"rpc_method":"system.health"'
}

greeting_count() {
  backend_log_count '"rpc_method":"greeting.hello"'
}

stream_count() {
  backend_log_count '"rpc_method":"bridra.smoke.stream"'
}

download_count() {
  backend_log_count '"http_method":"GET".*"surface":"file_transfer"'
}

download_resume_count() {
  backend_log_count 'server: smoke download resumed at offset 32768'
}

upload_verify_count() {
  backend_log_count '"rpc_method":"bridra.smoke.upload.verify"'
}

upload_resume_count() {
  backend_log_count 'server: smoke upload resumed at offset 32768'
}

start_backend() {
  listen_baseline=$(backend_log_count 'server: listening on ')
  echo "Starting Go HTTP backend on 127.0.0.1:$port..."
  "$server_path" \
    --listen "127.0.0.1:$port" \
    --token "$token" \
    --smoke-stream \
    --smoke-download \
    --smoke-download-resume \
    --smoke-upload-resume \
    --cors-origin '*' >>"$smoke_log" 2>&1 &
  server_pid=$!

  attempt=0
  while [ "$(backend_log_count 'server: listening on ')" -le "$listen_baseline" ]; do
    if ! kill -0 "$server_pid" 2>/dev/null; then
      redact_output <"$smoke_log" >&2
      wait "$server_pid" || true
      server_pid=
      echo "Go HTTP backend stopped before becoming ready." >&2
      exit 1
    fi
    attempt=$((attempt + 1))
    if [ "$attempt" -ge 100 ]; then
      redact_output <"$smoke_log" >&2
      echo "Go HTTP backend did not become ready." >&2
      exit 1
    fi
    sleep 0.1
  done
  record_stage backend_ready
}

stop_backend() {
  if [ -n "$server_pid" ] && kill -0 "$server_pid" 2>/dev/null; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  server_pid=
}

wait_for_test_pattern() {
  log_file=$1
  pattern=$2
  max_attempts=$3
  attempt=0
  while ! grep -Fq "$pattern" "$log_file"; do
    if [ "$((attempt % 5))" -eq 0 ]; then record_progress; fi
    if ! kill -0 "$test_pid" 2>/dev/null; then
      return 1
    fi
    attempt=$((attempt + 1))
    if [ "$attempt" -ge "$max_attempts" ]; then
      return 1
    fi
    sleep 0.2
  done
  record_progress
}

abort_test() {
  message=$1
  failure_reason=$message
  collect_diagnostics 1 || true
  test_status=1
  if kill -0 "$test_pid" 2>/dev/null; then
    kill "$test_pid" 2>/dev/null || true
  fi
  if wait "$test_pid"; then
    :
  else
    test_status=$?
  fi
  test_pid=
  redact_output <"$test_log" >&2
  redact_output <"$smoke_log" >&2
  echo "$message" >&2
  exit "$test_status"
}

start_backend

backend_url=http://10.0.2.2:$port/rpc
echo "Running Android Emulator RPC, streaming, transfer-resume, and reconnect integration test..."
# BRIDRA_FLUTTER intentionally contains a command and optional wrapper argument.
# shellcheck disable=SC2086
$flutter_command test integration_test/http_smoke_test.dart --verbose \
  -d "$device" \
  --dart-define="BRIDRA_BACKEND_URL=$backend_url" \
  --dart-define="BRIDRA_BACKEND_TOKEN=$token" \
  --dart-define="BRIDRA_SMOKE_CLIENT=Android Emulator" \
  --dart-define="BRIDRA_SMOKE_STREAM=true" \
  --dart-define="BRIDRA_SMOKE_DOWNLOAD=true" \
  --dart-define="BRIDRA_SMOKE_UPLOAD_RESUME=true" \
  --dart-define="BRIDRA_SMOKE_RECONNECT=true" >"$test_log" 2>&1 &
test_pid=$!
record_stage flutter_test_started

if ! wait_for_test_pattern \
  "$smoke_log" '"rpc_method":"bridra.smoke.stream"' 3000; then
  abort_test "Android Emulator did not complete its initial Streaming/Progress RPC."
fi
if ! wait_for_test_pattern \
  "$smoke_log" 'server: smoke download resumed at offset 32768' 3000; then
  abort_test "Android Emulator did not resume its interrupted managed download."
fi
if ! wait_for_test_pattern \
  "$smoke_log" 'server: smoke upload resumed at offset 32768' 3000; then
  abort_test "Android Emulator did not resume its interrupted managed upload."
fi
if ! wait_for_test_pattern \
  "$smoke_log" '"rpc_method":"bridra.smoke.upload.verify"' 3000; then
  abort_test "Go did not consume and verify the resumed managed upload."
fi

echo "Stopping Go HTTP backend to exercise the unavailable state..."
stop_backend
echo "Holding the backend offline for five seconds..."
sleep 5
if ! kill -0 "$test_pid" 2>/dev/null; then
  abort_test "Android Emulator did not remain active through the unavailable state."
fi

reconnect_health_baseline=$(health_count)
reconnect_greeting_baseline=$(greeting_count)
reconnect_stream_baseline=$(stream_count)
reconnect_download_baseline=$(download_count)
reconnect_download_resume_baseline=$(download_resume_count)
reconnect_upload_verify_baseline=$(upload_verify_count)
reconnect_upload_resume_baseline=$(upload_resume_count)
echo "Restarting Go HTTP backend for the reconnect action..."
start_backend
test_status=0
if wait "$test_pid"; then
  :
else
  test_status=$?
fi
test_pid=
record_progress
redact_output <"$test_log"
if [ "$test_status" -ne 0 ]; then
  redact_output <"$smoke_log" >&2
  exit "$test_status"
fi
if [ "$(health_count)" -le "$reconnect_health_baseline" ] ||
  [ "$(greeting_count)" -le "$reconnect_greeting_baseline" ] ||
  [ "$(stream_count)" -le "$reconnect_stream_baseline" ] ||
  [ "$(download_count)" -le "$reconnect_download_baseline" ] ||
  [ "$(download_resume_count)" -le "$reconnect_download_resume_baseline" ] ||
  [ "$(upload_verify_count)" -le "$reconnect_upload_verify_baseline" ] ||
  [ "$(upload_resume_count)" -le "$reconnect_upload_resume_baseline" ]; then
  redact_output <"$smoke_log" >&2
  echo "Reconnect did not complete new RPC, stream, download, and upload requests." >&2
  exit 1
fi

redact_output <"$smoke_log"
