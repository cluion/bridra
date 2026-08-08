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

server_path=${BRIDRA_SERVER_PATH:?BRIDRA_SERVER_PATH is required}
flutter_command=${BRIDRA_FLUTTER:-fvm flutter}
device=${BRIDRA_IOS_SIMULATOR_DEVICE:-}
port=${BRIDRA_IOS_SIMULATOR_PORT:-18080}
token=ios-simulator-smoke-token

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
smoke_log=${TMPDIR:-/tmp}/bridra-ios-simulator-smoke.$$.log

cleanup() {
  status=$?
  trap - EXIT INT TERM
  if [ -n "$server_pid" ] && kill -0 "$server_pid" 2>/dev/null; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  if [ "$booted_by_script" -eq 1 ]; then
    xcrun simctl shutdown "$device" >/dev/null 2>&1 || true
  fi
  rm -f "$smoke_log"
  exit "$status"
}
trap cleanup EXIT INT TERM

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

echo "Running iOS Simulator integration test on $device..."
test_status=0
# BRIDRA_FLUTTER intentionally contains a command and optional wrapper argument.
# shellcheck disable=SC2086
$flutter_command test integration_test/ios_simulator_smoke_test.dart \
  -d "$device" \
  --dart-define="BRIDRA_BACKEND_URL=http://127.0.0.1:$port/rpc" \
  --dart-define="BRIDRA_BACKEND_TOKEN=$token" || test_status=$?

cat "$smoke_log"
if [ "$test_status" -ne 0 ]; then
  exit "$test_status"
fi
grep -Fq '"rpc_method":"system.health"' "$smoke_log"
grep -Fq '"rpc_method":"greeting.hello"' "$smoke_log"
