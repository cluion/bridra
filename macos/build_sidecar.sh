#!/bin/sh
set -eu

project_root="$(cd "$PROJECT_DIR/.." && pwd -P)"
backend_dir="$project_root/backend"
output_dir="$TARGET_BUILD_DIR/$EXECUTABLE_FOLDER_PATH/libexec"
output="$output_dir/bridra_backend"
work_dir="$DERIVED_FILE_DIR/go-sidecar"
go_executable="${GO_EXECUTABLE:-$(command -v go || true)}"

if [ -z "$go_executable" ]; then
  echo "Go was not found on PATH. Install Go or set GO_EXECUTABLE." >&2
  exit 1
fi

if [ -z "${ARCHS:-}" ]; then
  echo "Xcode did not provide a target architecture." >&2
  exit 1
fi

mkdir -p "$output_dir" "$work_dir"
binaries=""

for xcode_arch in $ARCHS; do
  case "$xcode_arch" in
    arm64)
      go_arch="arm64"
      ;;
    x86_64)
      go_arch="amd64"
      ;;
    *)
      echo "Unsupported macOS architecture: $xcode_arch" >&2
      exit 1
      ;;
  esac

  binary="$work_dir/bridra_backend_$xcode_arch"
  (
    cd "$backend_dir"
    CGO_ENABLED=0 GOOS=darwin GOARCH="$go_arch" \
      "$go_executable" build -trimpath -o "$binary" ./cmd/sidecar
  )
  binaries="$binaries $binary"
done

set -- $binaries
if [ "$#" -eq 1 ]; then
  cp "$1" "$output"
else
  xcrun lipo -create "$@" -output "$output"
fi
chmod 755 "$output"

if [ "${CODE_SIGNING_ALLOWED:-NO}" = "YES" ]; then
  sign_identity="${EXPANDED_CODE_SIGN_IDENTITY:--}"
  /usr/bin/codesign --force --sign "$sign_identity" "$output"
fi
