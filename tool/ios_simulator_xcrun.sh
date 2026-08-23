#!/bin/sh

set -eu

real_xcrun=${BRIDRA_IOS_SIMULATOR_REAL_XCRUN:?BRIDRA_IOS_SIMULATOR_REAL_XCRUN is required}
launch_delay_seconds=${BRIDRA_IOS_SIMULATOR_LAUNCH_DELAY_SECONDS:-5}
launch_marker=${BRIDRA_IOS_SIMULATOR_LAUNCH_MARKER:-}

case "$launch_delay_seconds" in
  ''|*[!0-9]*)
    echo "BRIDRA_IOS_SIMULATOR_LAUNCH_DELAY_SECONDS must be a non-negative integer." >&2
    exit 1
    ;;
esac

# Flutter 3.44 starts the Simulator log reader asynchronously. A short launch
# barrier prevents Runner from publishing its one-shot VM service URL before
# that reader has connected. See flutter/flutter#181771.
if [ "${1:-}" = "simctl" ] && [ "${2:-}" = "launch" ] && [ "$launch_delay_seconds" -gt 0 ]; then
  if [ -n "$launch_marker" ]; then
    : >"$launch_marker"
  fi
  echo "Delaying iOS Simulator launch by ${launch_delay_seconds}s for Flutter log reader readiness."
  sleep "$launch_delay_seconds"
fi

exec "$real_xcrun" "$@"
