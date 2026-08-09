#!/bin/sh

set -eu

if [ "$(uname -s)" != "Darwin" ]; then
  echo "Physical iPhone smoke tests must run on macOS." >&2
  exit 1
fi

for dependency in xcrun xcodebuild route ipconfig lsof plutil; do
  command -v "$dependency" >/dev/null 2>&1 || {
    echo "$dependency is required for physical iPhone smoke tests." >&2
    exit 1
  }
done

server_path=${BRIDRA_SERVER_PATH:?BRIDRA_SERVER_PATH is required}
flutter_command=${BRIDRA_FLUTTER:-fvm flutter}
requested_device=${BRIDRA_IOS_DEVICE:-}
requested_port=${BRIDRA_IOS_DEVICE_PORT:-18081}
host_ip=${BRIDRA_IOS_DEVICE_HOST:-}
token=ios-device-smoke-token

case "$requested_port" in
  ''|*[!0-9]*)
    echo "BRIDRA_IOS_DEVICE_PORT must be numeric." >&2
    exit 1
    ;;
esac
if [ "$requested_port" -lt 1 ] || [ "$requested_port" -gt 65535 ]; then
  echo "BRIDRA_IOS_DEVICE_PORT must be between 1 and 65535." >&2
  exit 1
fi
if [ ! -x "$server_path" ]; then
  echo "Go HTTP backend is not executable: $server_path" >&2
  exit 1
fi

temporary_directory=$(mktemp -d "${TMPDIR:-/tmp}/bridra-ios-device-smoke.XXXXXX")
devices_json=$temporary_directory/devices.json
smoke_log=$temporary_directory/backend.log
test_log=$temporary_directory/flutter-test.log
launch_json=$temporary_directory/launch.json
server_pid=
test_pid=
app_pid=
: >"$smoke_log"
: >"$test_log"

terminate_app() {
  attempt=0
  while [ "$attempt" -lt 3 ]; do
    if xcrun devicectl device process terminate \
      --device "$device" \
      --pid "$app_pid" \
      --kill >/dev/null 2>&1; then
      app_pid=
      return
    fi
    attempt=$((attempt + 1))
    sleep 1
  done
  echo "Warning: could not terminate iPhone app process $app_pid." >&2
}

cleanup() {
  status=$?
  trap - EXIT INT TERM
  if [ -n "$test_pid" ] && kill -0 "$test_pid" 2>/dev/null; then
    kill "$test_pid" 2>/dev/null || true
    wait "$test_pid" 2>/dev/null || true
  fi
  if [ -n "$app_pid" ]; then
    terminate_app
  fi
  if [ -n "$server_pid" ] && kill -0 "$server_pid" 2>/dev/null; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  rm -f "$devices_json" "$smoke_log" "$test_log" "$launch_json"
  rmdir "$temporary_directory" 2>/dev/null || true
  exit "$status"
}
trap cleanup EXIT INT TERM

xcrun xcdevice list --timeout 5 >"$devices_json"

device=
device_name=
available_devices=0
index=0
while identifier=$(plutil -extract "$index.identifier" raw "$devices_json" 2>/dev/null); do
  platform=$(plutil -extract "$index.platform" raw "$devices_json" 2>/dev/null || true)
  simulator=$(plutil -extract "$index.simulator" raw "$devices_json" 2>/dev/null || true)
  available=$(plutil -extract "$index.available" raw "$devices_json" 2>/dev/null || true)
  name=$(plutil -extract "$index.name" raw "$devices_json" 2>/dev/null || true)

  if [ "$platform" = "com.apple.platform.iphoneos" ] &&
    [ "$simulator" = "false" ] &&
    [ "$available" = "true" ]; then
    if [ -n "$requested_device" ] && [ "$identifier" = "$requested_device" ]; then
      device=$identifier
      device_name=$name
    elif [ -z "$requested_device" ]; then
      available_devices=$((available_devices + 1))
      if [ "$available_devices" -eq 1 ]; then
        device=$identifier
        device_name=$name
      fi
    fi
  fi
  index=$((index + 1))
done

if [ -n "$requested_device" ] && [ -z "$device" ]; then
  echo "Physical iPhone is unavailable: $requested_device" >&2
  exit 1
fi
if [ -z "$requested_device" ] && [ "$available_devices" -eq 0 ]; then
  echo "No available physical iPhone was found." >&2
  exit 1
fi
if [ -z "$requested_device" ] && [ "$available_devices" -gt 1 ]; then
  echo "Multiple physical iPhones are available; set DEVICE=<flutter-device-id>." >&2
  exit 1
fi

if [ -z "$host_ip" ]; then
  network_interface=$(route -n get default 2>/dev/null | awk '
    /interface:/ { print $2; exit }
  ')
  if [ -z "$network_interface" ]; then
    echo "Could not determine the Mac's default network interface." >&2
    exit 1
  fi
  host_ip=$(ipconfig getifaddr "$network_interface" 2>/dev/null || true)
fi
if [ -z "$host_ip" ]; then
  echo "Could not determine a LAN address; set IOS_DEVICE_HOST=<address>." >&2
  exit 1
fi

port=$requested_port
last_port=$((requested_port + 99))
if [ "$last_port" -gt 65535 ]; then
  last_port=65535
fi
while lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; do
  if [ "$port" -ge "$last_port" ]; then
    echo "No free TCP port found from $requested_port through $last_port." >&2
    exit 1
  fi
  port=$((port + 1))
done
if [ "$port" -ne "$requested_port" ]; then
  echo "Port $requested_port is busy; using $port."
fi

development_team=$(
  xcodebuild \
    -workspace ios/Runner.xcworkspace \
    -scheme Runner \
    -configuration Profile \
    -showBuildSettings 2>/dev/null |
    awk -F ' = ' '/^[[:space:]]*DEVELOPMENT_TEAM = / { print $2; exit }'
)
if [ -z "$development_team" ]; then
  echo "No Apple Development Team is configured for the Runner target." >&2
  echo "Configure Xcode signing or create ignored ios/Flutter/Local.xcconfig." >&2
  exit 1
fi

echo "Using physical iPhone $device_name ($device)."

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

start_backend() {
  listen_baseline=$(backend_log_count 'server: listening on ')
  echo "Starting Go HTTP backend on 0.0.0.0:$port for $host_ip..."
  "$server_path" \
    --listen "0.0.0.0:$port" \
    --token "$token" \
    --smoke-stream \
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

backend_url=http://$host_ip:$port/rpc
echo "Allow the Local Network prompt on the iPhone if this bundle ID is new."
echo "Running physical iPhone Health, Greeting, Streaming/Progress, and reconnect integration test..."
# BRIDRA_FLUTTER intentionally contains a command and optional wrapper argument.
# shellcheck disable=SC2086
$flutter_command drive \
  --keep-app-running \
  --driver=test_driver/integration_test.dart \
  --target=integration_test/ios_http_smoke_test.dart \
  -d "$device" \
  --dart-define="BRIDRA_BACKEND_URL=$backend_url" \
  --dart-define="BRIDRA_BACKEND_TOKEN=$token" \
  --dart-define="BRIDRA_IOS_SMOKE_CLIENT=Physical iPhone" \
  --dart-define="BRIDRA_IOS_SMOKE_STREAM=true" \
  --dart-define="BRIDRA_IOS_SMOKE_RECONNECT=true" >"$test_log" 2>&1 &
test_pid=$!

if ! wait_for_test_pattern \
  "$smoke_log" '"rpc_method":"bridra.smoke.stream"' 3000; then
  abort_test "Physical iPhone did not complete its initial Streaming/Progress RPC."
fi

echo "Stopping Go HTTP backend to exercise the unavailable state..."
stop_backend
echo "Holding the backend offline for five seconds..."
sleep 5
if ! kill -0 "$test_pid" 2>/dev/null; then
  abort_test "Physical iPhone did not remain active through the unavailable state."
fi

reconnect_health_baseline=$(health_count)
reconnect_greeting_baseline=$(greeting_count)
reconnect_stream_baseline=$(stream_count)
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
  [ "$(stream_count)" -le "$reconnect_stream_baseline" ]; then
  cat "$smoke_log" >&2
  echo "Reconnect did not complete new Health, Greeting, and Streaming RPCs." >&2
  exit 1
fi

echo "Building signed Profile app for standalone cold launches..."
# shellcheck disable=SC2086
$flutter_command build ios --profile \
  --dart-define="BRIDRA_BACKEND_URL=$backend_url" \
  --dart-define="BRIDRA_BACKEND_TOKEN=$token"

app_path=build/ios/iphoneos/Runner.app
if [ ! -d "$app_path" ]; then
  echo "Profile app was not produced at $app_path." >&2
  exit 1
fi
bundle_identifier=$(plutil -extract CFBundleIdentifier raw "$app_path/Info.plist")
if [ -z "$bundle_identifier" ]; then
  echo "Profile app does not contain a bundle identifier." >&2
  exit 1
fi

echo "Installing Profile app $bundle_identifier..."
xcrun devicectl device install app --device "$device" "$app_path"
echo "Waiting for the installed Profile app to settle..."
sleep 5

wait_for_new_health() {
  baseline=$1
  label=$2
  attempt=0
  while [ "$(health_count)" -le "$baseline" ]; do
    attempt=$((attempt + 1))
    if [ "$attempt" -ge 150 ]; then
      cat "$smoke_log" >&2
      echo "$label did not complete system.health within 30 seconds." >&2
      exit 1
    fi
    sleep 0.2
  done
}

cold_launch() {
  label=$1
  baseline=$(health_count)
  rm -f "$launch_json"
  echo "$label..."
  xcrun devicectl device process launch \
    --device "$device" \
    --terminate-existing \
    --json-output "$launch_json" \
    "$bundle_identifier"
  app_pid=$(plutil -extract result.process.processIdentifier raw "$launch_json")
  wait_for_new_health "$baseline" "$label"
}

cold_launch "Cold-launching Profile app without Flutter tooling"
cold_launch "Cold-launching Profile app a second time"

cat "$smoke_log"
echo "Physical iPhone smoke passed: Health, Greeting, Streaming/Progress, reconnect, and two Profile cold launches."
