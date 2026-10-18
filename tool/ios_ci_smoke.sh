#!/bin/bash

set -eu

if [[ "${GITHUB_ACTIONS:-}" != true || "${RUNNER_ENVIRONMENT:-}" != github-hosted ]]; then
  echo "iOS CI profiling is limited to ephemeral GitHub-hosted runners." >&2
  exit 1
fi
if [[ "$#" -ne 1 || ! "$1" =~ ^[0-9a-fA-F]{8}(-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}$ ]]; then
  echo "A validated iPhone Simulator UUID is required." >&2
  exit 1
fi

device=$1
script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
project_root=$(CDPATH='' cd -- "$script_dir/.." && pwd)
cd "$project_root"

timed_stage() {
  local stage=$1
  shift
  local status=0
  local TIMEFORMAT="Bridra iOS $stage seconds: %3R"
  time "$@" || status=$?
  echo "Bridra iOS $stage exit: $status"
  return "$status"
}

# Run the same prerequisites as make ios-simulator-smoke, once each, with
# foreground timing so a slow Xcode check cannot disappear inside the total.
timed_stage xcode-check make macos-check
timed_stage backend-build make backend-server-build
export BRIDRA_SERVER_PATH="$project_root/build/server/bridra_server"
export BRIDRA_IOS_SIMULATOR_DEVICE="$device"
export BRIDRA_IOS_SIMULATOR_PORT="${BRIDRA_IOS_SIMULATOR_PORT:-18080}"
export BRIDRA_FLUTTER="fvm flutter --verbose"
timed_stage transport-smoke "$script_dir/ios_simulator_smoke.sh"
