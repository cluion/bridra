#!/bin/bash

set -eu

if [[ "${GITHUB_ACTIONS:-}" != true || "${RUNNER_ENVIRONMENT:-}" != github-hosted ]]; then
  echo "iOS CI profiling is limited to ephemeral GitHub-hosted runners." >&2
  exit 1
fi
if [[ "$#" -lt 1 || "$#" -gt 2 || ! "$1" =~ ^[0-9a-fA-F]{8}(-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}$ ]]; then
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
  echo "Bridra iOS $stage starting."
  time "$@" || status=$?
  echo "Bridra iOS $stage exit: $status"
  return "$status"
}

if [[ "$#" -eq 2 ]]; then
  # Accept only this source, toolchain, selected Simulator and the unchanged
  # backend from the owned worker that already ran these checks during setup.
  python3 "$script_dir/ios_ci_prepare.py" verify --state "$2" --device "$device"
else
  timed_stage xcode-check make macos-check
  timed_stage backend-build make backend-server-build
fi
export BRIDRA_SERVER_PATH="$project_root/build/server/bridra_server"
export BRIDRA_IOS_SIMULATOR_DEVICE="$device"
export BRIDRA_IOS_SIMULATOR_PORT="${BRIDRA_IOS_SIMULATOR_PORT:-18080}"
export BRIDRA_FLUTTER="fvm flutter --verbose"
timed_stage transport-smoke "$script_dir/ios_simulator_smoke.sh"
