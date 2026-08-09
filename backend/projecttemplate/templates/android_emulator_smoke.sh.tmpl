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
server_pid=
test_pid=
: >"$smoke_log"
: >"$test_log"

cleanup() {
  status=$?
  trap - EXIT INT TERM
  if [ -n "$test_pid" ] && kill -0 "$test_pid" 2>/dev/null; then
    kill "$test_pid" 2>/dev/null || true
    wait "$test_pid" 2>/dev/null || true
  fi
  if [ -n "$server_pid" ] && kill -0 "$server_pid" 2>/dev/null; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  rm -f "$devices_file" "$smoke_log" "$test_log"
  rmdir "$temporary_directory" 2>/dev/null || true
  exit "$status"
}
trap cleanup EXIT INT TERM

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
      cat "$smoke_log" >&2
      wait "$server_pid" || true
      server_pid=
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
    if ! kill -0 "$test_pid" 2>/dev/null; then
      return 1
    fi
    attempt=$((attempt + 1))
    if [ "$attempt" -ge "$max_attempts" ]; then
      return 1
    fi
    sleep 0.2
  done
}

abort_test() {
  message=$1
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
  cat "$test_log" >&2
  cat "$smoke_log" >&2
  echo "$message" >&2
  exit "$test_status"
}

start_backend

backend_url=http://10.0.2.2:$port/rpc
echo "Running Android Emulator RPC, streaming, transfer-resume, and reconnect integration test..."
# BRIDRA_FLUTTER intentionally contains a command and optional wrapper argument.
# shellcheck disable=SC2086
$flutter_command test integration_test/http_smoke_test.dart \
  -d "$device" \
  --dart-define="BRIDRA_BACKEND_URL=$backend_url" \
  --dart-define="BRIDRA_BACKEND_TOKEN=$token" \
  --dart-define="BRIDRA_SMOKE_CLIENT=Android Emulator" \
  --dart-define="BRIDRA_SMOKE_STREAM=true" \
  --dart-define="BRIDRA_SMOKE_DOWNLOAD=true" \
  --dart-define="BRIDRA_SMOKE_UPLOAD_RESUME=true" \
  --dart-define="BRIDRA_SMOKE_RECONNECT=true" >"$test_log" 2>&1 &
test_pid=$!

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
cat "$test_log"
if [ "$test_status" -ne 0 ]; then
  cat "$smoke_log" >&2
  exit "$test_status"
fi
if [ "$(health_count)" -le "$reconnect_health_baseline" ] ||
  [ "$(greeting_count)" -le "$reconnect_greeting_baseline" ] ||
  [ "$(stream_count)" -le "$reconnect_stream_baseline" ] ||
  [ "$(download_count)" -le "$reconnect_download_baseline" ] ||
  [ "$(download_resume_count)" -le "$reconnect_download_resume_baseline" ] ||
  [ "$(upload_verify_count)" -le "$reconnect_upload_verify_baseline" ] ||
  [ "$(upload_resume_count)" -le "$reconnect_upload_resume_baseline" ]; then
  cat "$smoke_log" >&2
  echo "Reconnect did not complete new RPC, stream, download, and upload requests." >&2
  exit 1
fi

cat "$smoke_log"
