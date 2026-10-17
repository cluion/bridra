# iOS runner comparison

The optional `iOS XCTest experiment` workflow compares the existing Flutter-tool
HTTP smoke with Flutter's official native XCTest bridge. Required PR/tag checks,
unsigned iOS release builds and publication protection remain unchanged. The
default iOS check retains `make ios-simulator-smoke` until results are reviewed.

## Native harness

`tool/ios_xctest.py` configures the same HTTP/streaming/download/upload smoke,
builds through `xcodebuild build-for-testing`, then executes the resulting
`.xctestrun` with `test-without-building`. XCTest uses the SDK's official runner
header and symbols provided by its test host, avoiding duplicate static Swift
package linkage.

Native setup enables semantics and acknowledges readiness to a Dart `setUpAll`
before Flutter records its per-test semantics baseline. The semantics ownership
check remains enabled. This bootstrap imports the existing HTTP test without
changing its assertions or generated-consumer template.

Acceptance requires exactly one native test, one recognized Dart test, no skips,
and every existing backend receipt, including interrupted/resumed uploads and
downloads. The negative fixture deliberately fails a Dart assertion. It requires
Xcode's test-failure exit, one failed test and the exact intentional failure
marker. Empty tests, boot/build errors or unrelated assertions do not qualify.

Subprocess stages have elapsed-time limits and separate logs; timing uses a
monotonic clock. Source inputs, Xcode and compilation defines identify reusable
test artifacts. Code or toolchain changes require rebuilding. Simulator test
binaries never replace the release app.

## Hosted measurements

Run manually after the workflow exists on the default branch, or add the
`benchmark-ios` label to an experiment PR. Pushing commits alone does not run
this additional matrix. Normal Verify remains independent.

1. Two fresh `macos-15` jobs run Flutter and XCTest. SDK/Pub/Go cache namespaces
   include the run ID and variant, ensuring cold misses without deleting shared
   caches. Each runs the positive flow.
2. Two new hosted jobs restore those exact dependency caches. Missing warm caches
   fail the measurement. Native/Xcode build products are not transferred.
3. Each warm job runs twice. Before replay, only the selected simulator in that
   disposable hosted job is erased. XCTest reuses the source-identified binary;
   the Flutter control reruns its normal command. Local runs never erase devices.
4. A separate fresh runner restores the XCTest dependency cache and verifies
   intentional Dart-failure propagation. This keeps failure diagnostics outside
   performance measurements and avoids reusing a simulator after changing the
   test entrypoint. Its assertions and timeouts remain identical.

Cold/warm refers to dependency caches; each fresh VM still starts with a cold
native build and simulator. Replay separately tests recovery after simulator
reset. Six positive runs give initial evidence, not an established flake rate.

Compare uploaded `benchmark.json`, native `timing.json`, `.xcresult` and cache
logs. Record job execution minutes including cache/upload cleanup, and report
negative-contract job overhead separately. Durations are not billing estimates.
Windows can remain the full-matrix bottleneck after iOS improves.

## Cache completeness

The shared Flutter action caches a complete host profile: Linux/Android/Web,
macOS/iOS, or Windows. Every successful writer pre-caches its whole profile.
Version 2 keys prevent an earlier macOS-only writer from freezing an incomplete
iOS cache. Weekly warming uses the same action and profile.

macOS and iOS share the same Go dependency fingerprint, allowing iOS to restore
existing default-branch macOS compiler caches. Experiment caches are isolated
from normal verification. Credentials, test results, app binaries and release
candidates are not dependency-cache contents.
