[CmdletBinding()]
param(
    [ValidateSet(
        "run",
        "build",
        "smoke",
        "verify",
        "generate",
        "doctor",
        "release-prepare",
        "release-check",
        "cli-release"
    )]
    [string]$Task = "run",
    [string]$Version,
    [switch]$Final
)

$ErrorActionPreference = "Stop"
$ProjectRoot = Split-Path -Parent $PSScriptRoot

function Invoke-Checked {
    param(
        [Parameter(Mandatory = $true)]
        [string]$FilePath,
        [Parameter(ValueFromRemainingArguments = $true)]
        [string[]]$Arguments
    )

    & $FilePath @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$FilePath failed with exit code $LASTEXITCODE"
    }
}

function Get-WindowsArchitecture {
    if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") {
        return "arm64"
    }
    return "x64"
}

function Build-WindowsBundle {
    Push-Location (Join-Path $ProjectRoot "backend")
    try {
        Invoke-Checked -FilePath "go" -Arguments @(
            "run",
            "./cmd/bridra",
            "build",
            "windows",
            "--root",
            ".."
        )
    }
    finally {
        Pop-Location
    }
}

function Get-BundledSidecar {
    $architecture = Get-WindowsArchitecture
    return Join-Path $ProjectRoot `
        "build/windows/$architecture/runner/Release/libexec/bridra_backend.exe"
}

function Test-WindowsBundle {
    $sidecar = Get-BundledSidecar
    if (-not (Test-Path -PathType Leaf $sidecar)) {
        throw "Bundled Go sidecar not found: $sidecar"
    }

    $request = '{"id":"smoke","method":"system.health","meta":{"token":"smoke-token"}}'
    $response = $request | & $sidecar --token smoke-token
    if ($LASTEXITCODE -ne 0) {
        throw "Bundled Go sidecar failed with exit code $LASTEXITCODE"
    }

    $message = $response | ConvertFrom-Json
    if ($message.result.status -ne "ok" -or $message.result.protocolVersion -ne 1) {
        throw "Bundled Go sidecar returned an invalid health response"
    }
    $response
}

function Generate-Contract {
    param([switch]$Check)

    $arguments = @(
        "run",
        "./cmd/bridra",
        "generate",
        "--schema",
        "../schema/bridra.json",
        "--root",
        ".."
    )
    if ($Check) {
        $arguments += "--check"
    }

    Push-Location (Join-Path $ProjectRoot "backend")
    try {
        Invoke-Checked -FilePath "go" -Arguments $arguments
    }
    finally {
        Pop-Location
    }
}

function Test-BridraEnvironment {
    $arguments = @(
        "run",
        "./cmd/bridra",
        "doctor",
        "--root",
        ".."
    )

    Push-Location (Join-Path $ProjectRoot "backend")
    try {
        Invoke-Checked -FilePath "go" -Arguments $arguments
    }
    finally {
        Pop-Location
    }
}

function Test-LicenseCopies {
    $rootLicensePath = Join-Path $ProjectRoot "LICENSE"
    if (-not (Test-Path -PathType Leaf $rootLicensePath)) {
        throw "Root LICENSE not found: $rootLicensePath"
    }

    $rootLicense = Get-Content -Raw $rootLicensePath
    $copies = @(
        (Join-Path $ProjectRoot "backend/LICENSE"),
        (Join-Path $ProjectRoot "packages/bridra_flutter/LICENSE")
    )
    foreach ($copy in $copies) {
        if (-not (Test-Path -PathType Leaf $copy)) {
            throw "Publishable package LICENSE not found: $copy"
        }
        if ((Get-Content -Raw $copy) -cne $rootLicense) {
            throw "$copy must match the root LICENSE"
        }
    }
}

function Build-CLIRelease {
    Push-Location $ProjectRoot
    try {
        $commit = (& git describe --always --dirty --abbrev=12 `
            --match "__bridra_no_matching_tag__").Trim()
        if ($LASTEXITCODE -ne 0 -or -not $commit) {
            throw "Unable to resolve the release commit"
        }
        $buildDate = (& git show -s --format=%cI HEAD).Trim()
        if ($LASTEXITCODE -ne 0 -or -not $buildDate) {
            throw "Unable to resolve the release build date"
        }
    }
    finally {
        Pop-Location
    }

    Push-Location (Join-Path $ProjectRoot "backend")
    try {
        Invoke-Checked -FilePath "go" -Arguments @(
            "run",
            "./cmd/bridra-release",
            "--root",
            ".",
            "--output",
            (Join-Path $ProjectRoot "build/bridra/cli"),
            "--commit",
            $commit,
            "--build-date",
            $buildDate
        )
    }
    finally {
        Pop-Location
    }
}

function Invoke-ReleaseCommand {
    param(
        [Parameter(Mandatory = $true)]
        [ValidateSet("prepare", "check")]
        [string]$Action
    )

    $arguments = @(
        "run",
        "./cmd/bridra",
        "release",
        $Action,
        "--root",
        ".."
    )
    if ($Action -eq "prepare") {
        if (-not $Version) {
            throw "-Version is required for release-prepare"
        }
        $arguments += $Version
    }
    elseif ($Version) {
        $arguments += @("--version", $Version)
    }
    if ($Action -eq "check" -and $Final) {
        $arguments += "--final"
    }

    Push-Location (Join-Path $ProjectRoot "backend")
    try {
        Invoke-Checked -FilePath "go" -Arguments $arguments
    }
    finally {
        Pop-Location
    }

    if ($Action -eq "prepare") {
        Push-Location $ProjectRoot
        try {
            Invoke-Checked -FilePath "fvm" -Arguments @("flutter", "pub", "get")
        }
        finally {
            Pop-Location
        }
    }
}

function Verify-Project {
    $sidecar = Join-Path $ProjectRoot "build/sidecar/bridra_backend.exe"
    $server = Join-Path $ProjectRoot "build/server/bridra_server.exe"
    New-Item -ItemType Directory -Force `
        -Path (Split-Path -Parent $sidecar) | Out-Null
    New-Item -ItemType Directory -Force `
        -Path (Split-Path -Parent $server) | Out-Null

    Test-LicenseCopies
    Invoke-ReleaseCommand -Action "check"
    Test-BridraEnvironment
    Generate-Contract -Check

    Push-Location (Join-Path $ProjectRoot "backend")
    try {
        $unformatted = & gofmt -l .
        if ($LASTEXITCODE -ne 0) {
            throw "gofmt failed with exit code $LASTEXITCODE"
        }
        if ($unformatted) {
            throw "Go files need formatting:`n$($unformatted -join "`n")"
        }
        Invoke-Checked -FilePath "go" -Arguments @("vet", "./...")
        Invoke-Checked -FilePath "go" `
            -Arguments @("test", "./framework", "-run", "^TestPublic")
        Invoke-Checked -FilePath "go" -Arguments @("test", "./...")

        $previousCgo = $env:CGO_ENABLED
        try {
            $env:CGO_ENABLED = "0"
            Invoke-Checked -FilePath "go" `
                -Arguments @("build", "-trimpath", "-o", $sidecar, "./cmd/sidecar")
            Invoke-Checked -FilePath "go" `
                -Arguments @("build", "-trimpath", "-o", $server, "./cmd/server")
        }
        finally {
            $env:CGO_ENABLED = $previousCgo
        }
    }
    finally {
        Pop-Location
    }

    Push-Location $ProjectRoot
    try {
        Invoke-Checked -FilePath "fvm" -Arguments @(
            "dart",
            "format",
            "--output=none",
            "--set-exit-if-changed",
            "lib",
            "test",
            "packages/bridra_flutter/lib",
            "packages/bridra_flutter/test"
        )
        $previousSidecar = $env:BRIDRA_SIDECAR_PATH
        $previousServer = $env:BRIDRA_SERVER_PATH
        try {
            $env:BRIDRA_SIDECAR_PATH = $sidecar
            $env:BRIDRA_SERVER_PATH = $server
            Invoke-Checked -FilePath "fvm" `
                -Arguments @("flutter", "test")
        }
        finally {
            $env:BRIDRA_SIDECAR_PATH = $previousSidecar
            $env:BRIDRA_SERVER_PATH = $previousServer
        }
        Push-Location (Join-Path $ProjectRoot "packages/bridra_flutter")
        try {
            $previousPackageSidecar = $env:BRIDRA_SIDECAR_PATH
            try {
                $env:BRIDRA_SIDECAR_PATH = $sidecar
                Invoke-Checked -FilePath "fvm" `
                    -Arguments @("flutter", "test")
            }
            finally {
                $env:BRIDRA_SIDECAR_PATH = $previousPackageSidecar
            }
            Invoke-Checked -FilePath "fvm" `
                -Arguments @("flutter", "analyze")
        }
        finally {
            Pop-Location
        }
        Invoke-Checked -FilePath "fvm" `
            -Arguments @("flutter", "analyze")
    }
    finally {
        Pop-Location
    }
}

switch ($Task) {
    "run" {
        Push-Location $ProjectRoot
        try {
            Invoke-Checked -FilePath "fvm" `
                -Arguments @("flutter", "run", "-d", "windows")
        }
        finally {
            Pop-Location
        }
    }
    "build" {
        Build-WindowsBundle
    }
    "smoke" {
        Build-WindowsBundle
        Test-WindowsBundle
    }
    "verify" {
        Verify-Project
    }
    "generate" {
        Generate-Contract
    }
    "doctor" {
        Test-BridraEnvironment
    }
    "release-prepare" {
        Invoke-ReleaseCommand -Action "prepare"
    }
    "release-check" {
        Invoke-ReleaseCommand -Action "check"
    }
    "cli-release" {
        Build-CLIRelease
    }
}
