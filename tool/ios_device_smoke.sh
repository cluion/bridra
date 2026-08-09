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
launch_json=$temporary_directory/launch.json
server_pid=
app_pid=

cleanup() {
  status=$?
  trap - EXIT INT TERM
  if [ -n "$app_pid" ]; then
    xcrun devicectl device process terminate \
      --device "$device" \
      --pid "$app_pid" \
      --kill >/dev/null 2>&1 || true
  fi
  if [ -n "$server_pid" ] && kill -0 "$server_pid" 2>/dev/null; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  rm -f "$devices_json" "$smoke_log" "$launch_json"
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
echo "Starting Go HTTP backend on 0.0.0.0:$port for $host_ip..."
"$server_path" \
  --listen "0.0.0.0:$port" \
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

backend_url=http://$host_ip:$port/rpc
echo "Allow the Local Network prompt on the iPhone if this bundle ID is new."
echo "Running physical iPhone Health and Greeting integration test..."
test_status=0
# BRIDRA_FLUTTER intentionally contains a command and optional wrapper argument.
# shellcheck disable=SC2086
$flutter_command test integration_test/ios_http_smoke_test.dart \
  -d "$device" \
  --no-uninstall \
  --dart-define="BRIDRA_BACKEND_URL=$backend_url" \
  --dart-define="BRIDRA_BACKEND_TOKEN=$token" \
  --dart-define="BRIDRA_IOS_SMOKE_CLIENT=Physical iPhone" || test_status=$?
if [ "$test_status" -ne 0 ]; then
  cat "$smoke_log" >&2
  exit "$test_status"
fi
grep -Fq '"rpc_method":"system.health"' "$smoke_log"
grep -Fq '"rpc_method":"greeting.hello"' "$smoke_log"

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

health_count() {
  grep -c '"rpc_method":"system.health"' "$smoke_log" || true
}

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
echo "Physical iPhone smoke passed: Health, Greeting, and two Profile cold launches."
