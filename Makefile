FLUTTER ?= fvm flutter
DART ?= fvm dart
GO ?= go
FVM ?= fvm
BRIDRA_FLUTTER_PACKAGE := packages/bridra_flutter
BRIDRA := cd backend && $(GO) run ./cmd/bridra

ifeq ($(OS),Windows_NT)
HOST_OS := Windows
HOST_ARCH := $(PROCESSOR_ARCHITECTURE)
EXECUTABLE_SUFFIX := .exe
else
HOST_OS := $(shell uname -s)
HOST_ARCH := $(shell uname -m)
EXECUTABLE_SUFFIX :=
endif

SIDECAR := $(CURDIR)/build/sidecar/bridra_backend$(EXECUTABLE_SUFFIX)
HTTP_SERVER := $(CURDIR)/build/server/bridra_server$(EXECUTABLE_SUFFIX)
BACKEND_TOKEN ?= dev-token
BACKEND_LISTEN ?= 127.0.0.1:8080
BACKEND_CORS_ORIGIN ?= *
BACKEND_URL ?=
IOS_SIMULATOR_PORT ?= 18080
DART_DEFINES := --dart-define='BRIDRA_BACKEND_TOKEN=$(BACKEND_TOKEN)'
ifneq ($(strip $(BACKEND_URL)),)
DART_DEFINES += --dart-define='BRIDRA_BACKEND_URL=$(BACKEND_URL)'
endif
LINUX_ARCH ?= $(if $(filter x86_64 amd64,$(HOST_ARCH)),x64,$(if $(filter arm64 aarch64,$(HOST_ARCH)),arm64,unsupported))
LINUX_BUNDLE := $(CURDIR)/build/linux/$(LINUX_ARCH)/release/bundle
LINUX_SIDECAR := $(LINUX_BUNDLE)/libexec/bridra_backend
MACOS_APP := $(CURDIR)/build/macos/Build/Products/Release/bridra.app
MACOS_SIDECAR := $(MACOS_APP)/Contents/MacOS/libexec/bridra_backend
CLI_RELEASE_OUTPUT ?= $(CURDIR)/build/bridra/cli
CLI_RELEASE_COMMIT ?= $(shell git describe --always --dirty --abbrev=12 --match '__bridra_no_matching_tag__')
CLI_RELEASE_DATE ?= $(shell git show -s --format=%cI HEAD)
COVERAGE_DIR := $(CURDIR)/coverage
RUNTIME_FUZZ_TIME ?= 15s
RUNTIME_STRESS_CYCLES ?= 50
RUNTIME_STRESS_REPEATS ?= 5
RUNTIME_RESOURCE_MAX_GOROUTINE_GROWTH ?= 4
RUNTIME_RESOURCE_MAX_HEAP_GROWTH_MIB ?= 8
RUNTIME_RESOURCE_MAX_FD_GROWTH ?= 4
RUNTIME_RESOURCE_MAX_RSS_GROWTH_MIB ?= 32

.PHONY: help setup doctor generate codegen-check license-check format backend-build backend-server-build backend-serve backend-format backend-test backend-public-api-test backend-sql-store-test backend-sql-job-store-test backend-vet transport-benchmark \
	flutter-format flutter-package-test flutter-web-test flutter-test analyze verify coverage backend-coverage flutter-package-coverage flutter-app-coverage coverage-check linux-check linux-run linux-build \
	linux-smoke macos-check macos-run macos-build macos-smoke windows-run \
	windows-build windows-smoke windows-verify android-run android-build \
	ios-run ios-build ios-simulator-build ios-simulator-smoke web-run web-build remote-release-check \
	release-prepare release-check cli-release runtime-fuzz runtime-resources runtime-stress run

help:
	@echo "make setup        Install the pinned Flutter SDK and project dependencies"
	@echo "make doctor       Check Go, FVM, and the pinned Flutter SDK"
	@echo "make generate     Generate Go and Dart APIs from schema/bridra.json"
	@echo "make license-check Verify publishable packages carry the root MIT license"
	@echo "make verify       Run format checks, Go tests, Flutter tests, and analysis"
	@echo "make coverage     Generate reports and enforce coverage non-regression floors"
	@echo "make runtime-fuzz Fuzz Sidecar and HTTP RPC parsing"
	@echo "make runtime-resources Check Runtime resource growth and orphan processes"
	@echo "make runtime-stress Repeat Runtime lifecycle, concurrency, and recovery checks"
	@echo "make transport-benchmark Measure JSON, binary-pipe, and managed-file transport costs"
	@echo "make run          Run the starter on the current desktop platform"
	@echo "make macos-run    Run the starter on macOS"
	@echo "make macos-build  Build a universal macOS app with its Go sidecar"
	@echo "make macos-smoke  Build the macOS app and exercise the bundled sidecar"
	@echo "make linux-run    Run the starter on Linux"
	@echo "make linux-build  Build a Linux release bundle with its Go sidecar"
	@echo "make linux-smoke  Build the bundle and exercise the bundled sidecar"
	@echo "make windows-run  Run the starter on Windows"
	@echo "make windows-build Build a Windows release bundle with its Go sidecar"
	@echo "make windows-smoke Build the Windows bundle and exercise the sidecar"
	@echo "make windows-verify Run all verification checks on Windows"
	@echo "make backend-serve Run the Go HTTP backend for mobile and Web"
	@echo "make android-run  Run on an Android device (DEVICE=<id> optional)"
	@echo "make android-build Build an Android release APK (HTTPS URL required)"
	@echo "make ios-run      Run on an iOS device (DEVICE=<id> optional)"
	@echo "make ios-build    Build an unsigned iOS release (HTTPS URL required)"
	@echo "make ios-simulator-smoke Exercise real HTTP RPCs in an iOS Simulator"
	@echo "make web-run      Run the Web app in Chrome"
	@echo "make web-build    Build a Web release (HTTPS URL required)"
	@echo "make release-prepare VERSION=x.y.z Synchronize one release version"
	@echo "make release-check Verify all managed release versions agree"
	@echo "make cli-release  Build deterministic CLI archives and checksums"
	@echo "make format       Format Go and Dart sources"

setup:
	@command -v $(FVM) >/dev/null 2>&1 || \
		(echo "Missing FVM. Install it from https://fvm.app/documentation/getting-started/installation"; exit 1)
	$(FVM) install
	$(FLUTTER) pub get
	cd $(BRIDRA_FLUTTER_PACKAGE) && $(FLUTTER) pub get
	@$(MAKE) doctor

doctor:
	cd backend && $(GO) run ./cmd/bridra doctor --root ..

generate:
	cd backend && $(GO) run ./cmd/bridra generate --schema ../schema/bridra.json --root ..

codegen-check:
	cd backend && $(GO) run ./cmd/bridra generate --schema ../schema/bridra.json --root .. --check

license-check:
	@cmp -s LICENSE backend/LICENSE || \
		(echo "backend/LICENSE must match the root LICENSE."; exit 1)
	@cmp -s LICENSE $(BRIDRA_FLUTTER_PACKAGE)/LICENSE || \
		(echo "$(BRIDRA_FLUTTER_PACKAGE)/LICENSE must match the root LICENSE."; exit 1)

format:
	cd backend && gofmt -w .
	$(DART) format lib test integration_test $(BRIDRA_FLUTTER_PACKAGE)/lib $(BRIDRA_FLUTTER_PACKAGE)/test

backend-build:
	mkdir -p $(dir $(SIDECAR))
	cd backend && CGO_ENABLED=0 $(GO) build -trimpath -o $(SIDECAR) ./cmd/sidecar
	$(MAKE) backend-server-build

backend-server-build:
	mkdir -p $(dir $(HTTP_SERVER))
	cd backend && CGO_ENABLED=0 $(GO) build -trimpath -o $(HTTP_SERVER) ./cmd/server

backend-serve: backend-server-build
	BRIDRA_BACKEND_TOKEN='$(BACKEND_TOKEN)' $(HTTP_SERVER) --listen $(BACKEND_LISTEN) --cors-origin '$(BACKEND_CORS_ORIGIN)'

backend-format:
	@test -z "$$(cd backend && gofmt -l .)" || \
		(cd backend && gofmt -d . && echo "Go files need formatting." && exit 1)

backend-test:
	cd backend && $(GO) test -race ./...

backend-public-api-test:
	cd backend && $(GO) test -race ./framework -run '^TestPublic'

backend-sql-store-test:
	cd backend/integration/sqljobstore && $(GO) test -race ./...

backend-sql-job-store-test: backend-sql-store-test

backend-vet:
	cd backend && $(GO) vet ./...

transport-benchmark:
	cd backend && $(GO) test ./framework -run '^$$' -bench '^BenchmarkTransport' -benchmem -benchtime=200ms -count=3

runtime-fuzz:
	cd backend && $(GO) test ./framework -run '^$$' -fuzz '^FuzzSidecarServerInput$$' -fuzztime='$(RUNTIME_FUZZ_TIME)'
	cd backend && $(GO) test ./framework -run '^$$' -fuzz '^FuzzHTTPRPCInput$$' -fuzztime='$(RUNTIME_FUZZ_TIME)'

runtime-resources: backend-build
	cd backend && BRIDRA_STRESS=1 BRIDRA_STRESS_CYCLES='$(RUNTIME_STRESS_CYCLES)' BRIDRA_RESOURCE_MAX_GOROUTINE_GROWTH='$(RUNTIME_RESOURCE_MAX_GOROUTINE_GROWTH)' BRIDRA_RESOURCE_MAX_HEAP_GROWTH_MIB='$(RUNTIME_RESOURCE_MAX_HEAP_GROWTH_MIB)' BRIDRA_RESOURCE_MAX_FD_GROWTH='$(RUNTIME_RESOURCE_MAX_FD_GROWTH)' $(GO) test -race -v ./framework -run '^TestRuntimeResourceStability$$' -count=1 -timeout=10m
	BRIDRA_SIDECAR_PATH=$(SIDECAR) BRIDRA_RESOURCE_STRESS=1 BRIDRA_STRESS_CYCLES='$(RUNTIME_STRESS_CYCLES)' BRIDRA_RESOURCE_MAX_RSS_GROWTH_MIB='$(RUNTIME_RESOURCE_MAX_RSS_GROWTH_MIB)' BRIDRA_RESOURCE_MAX_FD_GROWTH='$(RUNTIME_RESOURCE_MAX_FD_GROWTH)' $(FLUTTER) test test/runtime_resource_stress_test.dart --plain-name 'real Sidecar resources stay bounded across load and restart cycles'

runtime-stress: runtime-fuzz runtime-resources
	cd backend && BRIDRA_STRESS=1 BRIDRA_STRESS_CYCLES='$(RUNTIME_STRESS_CYCLES)' $(GO) test -race ./framework -run '^TestRuntimeStress' -count=1 -timeout=10m
	cd backend && $(GO) test -race ./framework -run '^(TestServerDispatchesRequestsConcurrentlyAndDrains|TestServerBoundsConcurrentRequests|TestServerCancellationReachesActiveAndPendingRequests|TestServerRejectsRequestsBeyondPendingLimit|TestServerStreamWindowAppliesBackpressureUntilAcknowledged|TestServerCancelsBackpressuredStreamsWhenInputCloses|TestParentProcessContextCancelsWhenParentExits)$$' -count='$(RUNTIME_STRESS_REPEATS)' -timeout=10m
	cd backend/integration/sqljobstore && $(GO) test -race ./... -run '^(TestSQLiteStoresCoordinateReservationAndLifecycle|TestSQLiteSchedulerStoresCoordinateReservationAndLifecycle|TestPostgreSQLStoresCoordinateReservationAndLifecycle|TestPostgreSQLSchedulerStoresCoordinateReservationAndLifecycle)$$' -count='$(RUNTIME_STRESS_REPEATS)' -timeout=10m
	cd backend && $(GO) test -race ./integration/redisjobstore -run '^(TestRedisStoresCoordinateReservationAndLifecycle|TestRedisSchedulerStoresCoordinateReservationAndLifecycle)$$' -count='$(RUNTIME_STRESS_REPEATS)' -timeout=10m
	cd $(BRIDRA_FLUTTER_PACKAGE) && BRIDRA_STRESS=1 BRIDRA_STRESS_CYCLES='$(RUNTIME_STRESS_CYCLES)' $(FLUTTER) test test/sidecar_client_test.dart --plain-name 'stress repeatedly crashes and recovers without replay'

flutter-format:
	$(DART) format --output=none --set-exit-if-changed lib test integration_test $(BRIDRA_FLUTTER_PACKAGE)/lib $(BRIDRA_FLUTTER_PACKAGE)/test

flutter-package-test: backend-build
	cd $(BRIDRA_FLUTTER_PACKAGE) && BRIDRA_SIDECAR_PATH=$(SIDECAR) $(FLUTTER) test

flutter-web-test:
	cd $(BRIDRA_FLUTTER_PACKAGE) && $(FLUTTER) test --platform chrome \
		test/default_connector_web_test.dart \
		test/rpc_file_test.dart \
		test/http_rpc_client_test.dart

flutter-test: backend-build
	BRIDRA_SIDECAR_PATH=$(SIDECAR) BRIDRA_SERVER_PATH=$(HTTP_SERVER) $(FLUTTER) test

analyze:
	$(FLUTTER) analyze
	cd $(BRIDRA_FLUTTER_PACKAGE) && $(FLUTTER) analyze

backend-coverage:
	mkdir -p $(COVERAGE_DIR)
	cd backend && $(GO) test -covermode=atomic -coverprofile=$(COVERAGE_DIR)/go.out ./...

flutter-package-coverage: backend-build
	cd $(BRIDRA_FLUTTER_PACKAGE) && BRIDRA_SIDECAR_PATH=$(SIDECAR) $(FLUTTER) test --coverage

flutter-app-coverage: backend-build
	BRIDRA_SIDECAR_PATH=$(SIDECAR) BRIDRA_SERVER_PATH=$(HTTP_SERVER) $(FLUTTER) test --coverage

coverage-check: backend-coverage flutter-package-coverage flutter-app-coverage
	cd backend && $(GO) run ./tool/coveragecheck --root ..

coverage: coverage-check flutter-web-test

release-prepare:
	@test -n "$(VERSION)" || \
		(echo "VERSION is required, for example: make release-prepare VERSION=0.1.1"; exit 1)
	$(BRIDRA) release prepare '$(VERSION)' --root ..
	$(FLUTTER) pub get

release-check:
	$(BRIDRA) release check --root .. $(if $(strip $(VERSION)),--version '$(VERSION)') $(if $(filter 1 true yes,$(FINAL)),--final)

cli-release:
	cd backend && $(GO) run ./cmd/bridra-release \
		--root . \
		--output '$(CLI_RELEASE_OUTPUT)' \
		--commit '$(CLI_RELEASE_COMMIT)' \
		--build-date '$(CLI_RELEASE_DATE)'

verify: license-check release-check doctor codegen-check backend-format backend-vet backend-public-api-test backend-test backend-sql-store-test flutter-format flutter-package-test flutter-web-test flutter-test analyze

linux-check:
	@test "$(HOST_OS)" = "Linux" || \
		(echo "Linux desktop builds must run on a Linux host." && exit 1)
	@test "$(LINUX_ARCH)" != "unsupported" || \
		(echo "Unsupported Linux architecture: $(HOST_ARCH)" && exit 1)

linux-run: linux-check backend-build
	BRIDRA_SIDECAR_PATH=$(SIDECAR) $(FLUTTER) run -d linux

linux-build: linux-check
	$(BRIDRA) build linux --root ..

linux-smoke: linux-build
	@response="$$(printf '%s\n' '{"id":"smoke","method":"system.health","meta":{"token":"smoke-token"}}' | \
		$(LINUX_SIDECAR) --token smoke-token)"; \
		echo "$$response"; \
		echo "$$response" | grep -q '"status":"ok"'

macos-check:
	@test "$(HOST_OS)" = "Darwin" || \
		(echo "macOS desktop builds must run on a macOS host." && exit 1)
	@developer_dir="$${DEVELOPER_DIR:-$$(xcode-select -p 2>/dev/null || true)}"; \
		if [ "$$developer_dir" = "/Library/Developer/CommandLineTools" ] && \
			[ -d "/Applications/Xcode.app/Contents/Developer" ]; then \
			echo "Xcode is installed but not selected. Run:"; \
			echo "  sudo xcode-select --switch /Applications/Xcode.app/Contents/Developer"; \
			echo "  sudo xcodebuild -runFirstLaunch"; \
			exit 1; \
		fi
	@xcodebuild -version >/dev/null 2>&1 || \
		(echo "Full Xcode is required. Install Xcode and select its developer directory." && exit 1)

macos-run: macos-check
	$(FLUTTER) run -d macos

macos-build: macos-check
	$(BRIDRA) build macos --root ..
	@archs="$$(xcrun lipo -archs $(MACOS_SIDECAR))"; \
		echo "Go sidecar architectures: $$archs"; \
		echo "$$archs" | grep -q arm64; \
		echo "$$archs" | grep -q x86_64

macos-smoke: macos-build
	@response="$$(printf '%s\n' '{"id":"smoke","method":"system.health","meta":{"token":"smoke-token"}}' | \
		$(MACOS_SIDECAR) --token smoke-token)"; \
		echo "$$response"; \
		echo "$$response" | grep -q '"status":"ok"'

windows-run:
	powershell.exe -NoProfile -ExecutionPolicy Bypass -File tool/windows.ps1 -Task run

windows-build:
	powershell.exe -NoProfile -ExecutionPolicy Bypass -File tool/windows.ps1 -Task build

windows-smoke:
	powershell.exe -NoProfile -ExecutionPolicy Bypass -File tool/windows.ps1 -Task smoke

windows-verify:
	powershell.exe -NoProfile -ExecutionPolicy Bypass -File tool/windows.ps1 -Task verify

DEVICE_ARG := $(if $(strip $(DEVICE)),-d $(DEVICE),)

remote-release-check:
	@case "$(BACKEND_URL)" in \
		https://*) ;; \
		*) echo "Release builds require BACKEND_URL=https://..."; exit 1 ;; \
	esac

android-run:
	$(FLUTTER) run $(DEVICE_ARG) $(DART_DEFINES)

android-build: remote-release-check
	$(BRIDRA) build android --root .. \
		--backend-url '$(BACKEND_URL)' --token '$(BACKEND_TOKEN)'

ios-run: macos-check
	$(FLUTTER) run $(DEVICE_ARG) $(DART_DEFINES)

ios-build: macos-check remote-release-check
	$(BRIDRA) build ios --root .. \
		--backend-url '$(BACKEND_URL)' --token '$(BACKEND_TOKEN)'

ios-simulator-build: macos-check
	$(FLUTTER) build ios --simulator --debug $(DART_DEFINES)

ios-simulator-smoke: macos-check backend-server-build
	BRIDRA_SERVER_PATH='$(HTTP_SERVER)' \
		BRIDRA_FLUTTER='$(FLUTTER)' \
		BRIDRA_IOS_SIMULATOR_DEVICE='$(DEVICE)' \
		BRIDRA_IOS_SIMULATOR_PORT='$(IOS_SIMULATOR_PORT)' \
		./tool/ios_simulator_smoke.sh

web-run:
	$(FLUTTER) run -d chrome $(DART_DEFINES)

web-build: remote-release-check
	$(BRIDRA) build web --root .. \
		--backend-url '$(BACKEND_URL)' --token '$(BACKEND_TOKEN)'

ifeq ($(HOST_OS),Windows)
run: windows-run
else ifeq ($(HOST_OS),Darwin)
run: macos-run
else ifeq ($(HOST_OS),Linux)
run: linux-run
else
run:
	@echo "Unsupported desktop host: $(HOST_OS)"; exit 1
endif
