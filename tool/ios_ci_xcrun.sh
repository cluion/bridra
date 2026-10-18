#!/bin/sh

set -eu

if [ "${GITHUB_ACTIONS:-}" != true ] || [ "${RUNNER_ENVIRONMENT:-}" != github-hosted ]; then
  echo "iOS build scheduling is limited to ephemeral GitHub-hosted runners." >&2
  exit 1
fi
jobs=${BRIDRA_IOS_CI_XCODE_JOBS:-}
case "$jobs" in
  1|2|3) ;;
  *) echo "The hosted iOS build limit must be 1, 2 or 3." >&2; exit 1 ;;
esac
real_xcrun=${BRIDRA_IOS_CI_REAL_XCRUN:?BRIDRA_IOS_CI_REAL_XCRUN is required}
case "$real_xcrun" in
  /*) ;;
  *) echo "The original xcrun must be an absolute executable path." >&2; exit 1 ;;
esac
[ -x "$real_xcrun" ] || exit 1

if [ "${1:-}" = xcodebuild ]; then
  result_bundle=0
  simulator=0
  existing_jobs=0
  previous=
  for argument in "$@"; do
    if [ "$previous" = -sdk ] && [ "$argument" = iphonesimulator ]; then simulator=1; fi
    if [ "$argument" = -resultBundlePath ]; then result_bundle=1; fi
    if [ "$argument" = -jobs ]; then existing_jobs=1; fi
    previous=$argument
  done
  # Flutter's actual Simulator build has both flags; metadata queries and the
  # original launch/watchdog wrapper must pass through without extra work.
  if [ "$result_bundle" = 1 ] && [ "$simulator" = 1 ]; then
    if [ "$existing_jobs" = 1 ]; then
      echo "Refusing conflicting Xcode build scheduling arguments." >&2
      exit 1
    fi
    shift
    echo "Bridra iOS Xcode build concurrency: $jobs" >&2
    exec "$real_xcrun" xcodebuild -jobs "$jobs" "$@"
  fi
fi
exec "$real_xcrun" "$@"
