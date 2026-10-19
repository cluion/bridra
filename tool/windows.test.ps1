[CmdletBinding()]
param([string]$ScriptPath)

$ErrorActionPreference = "Stop"
if (-not $ScriptPath) {
    $ScriptPath = Join-Path $PSScriptRoot "windows.ps1"
}
$fixture = Join-Path ([IO.Path]::GetTempPath()) ("bridra-windows-contract-" + [Guid]::NewGuid())
$savedEnvironment = @{}
foreach ($name in @("BRIDRA_WINDOWS_TIMINGS_PATH", "GITHUB_STEP_SUMMARY", "CGO_ENABLED",
        "BRIDRA_SIDECAR_PATH", "BRIDRA_SERVER_PATH")) {
    $savedEnvironment[$name] = [Environment]::GetEnvironmentVariable($name)
}

function Assert-Contract {
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) { throw $Message }
}

function Invoke-FakeTool {
    param([string]$Name, [string[]]$Arguments)
    $call = [PSCustomObject]@{ name = $Name; arguments = @($Arguments); directory = (Get-Location).Path }
    $global:BridraWindowsContractCalls.Add($call)
    $global:LASTEXITCODE = 0
    if (((@($Name) + $Arguments) -join " ") -eq $global:BridraWindowsContractFailure) {
        $global:LASTEXITCODE = 17
    }
}

function go { Invoke-FakeTool -Name "go" -Arguments $args }
function fvm { Invoke-FakeTool -Name "fvm" -Arguments $args }
function gofmt { Invoke-FakeTool -Name "gofmt" -Arguments $args }

try {
    foreach ($directory in @("tool", "backend/integration/sqljobstore", "packages/bridra_flutter")) {
        New-Item -ItemType Directory -Force -Path (Join-Path $fixture $directory) | Out-Null
    }
    Copy-Item $ScriptPath (Join-Path $fixture "tool/windows.ps1")
    foreach ($license in @("LICENSE", "backend/LICENSE", "packages/bridra_flutter/LICENSE")) {
        [IO.File]::WriteAllText((Join-Path $fixture $license), "Fixture license")
    }
    $env:CGO_ENABLED = "contract-cgo"
    $env:BRIDRA_SIDECAR_PATH = "contract-sidecar"
    $env:BRIDRA_SERVER_PATH = "contract-server"
    $scenarios = @(
        @{ name = "standalone"; delegated = $false; failure = ""; timed = $true },
        @{ name = "required-core"; delegated = $true; failure = ""; timed = $true },
        @{ name = "go-failure"; delegated = $true; failure = "go test ./..."; timed = $true },
        @{ name = "flutter-failure"; delegated = $true; failure = "fvm flutter test"; timed = $true },
        @{ name = "untimed-standalone"; delegated = $false; failure = ""; timed = $false }
    )
    foreach ($scenario in $scenarios) {
        $global:BridraWindowsContractCalls = [Collections.Generic.List[object]]::new()
        $global:BridraWindowsContractFailure = $scenario.failure
        $timings = Join-Path $fixture ($scenario.name + ".jsonl")
        $summary = Join-Path $fixture ($scenario.name + ".md")
        $env:BRIDRA_WINDOWS_TIMINGS_PATH = if ($scenario.timed) { $timings } else { $null }
        $env:GITHUB_STEP_SUMMARY = $summary
        $caught = $null
        $location = (Get-Location).Path
        try {
            $parameters = @{ Task = "verify" }
            if ($scenario.delegated) { $parameters.SharedChecksInCore = $true }
            & (Join-Path $fixture "tool/windows.ps1") @parameters
        }
        catch { $caught = $_ }
        Assert-Contract (($null -ne $caught) -eq [bool]$scenario.failure) "$($scenario.name): wrong failure result: $caught"
        Assert-Contract ((Get-Location).Path -eq $location) "Working directory was not restored"
        Assert-Contract ($env:CGO_ENABLED -eq "contract-cgo") "CGO state leaked"
        Assert-Contract ($env:BRIDRA_SIDECAR_PATH -eq "contract-sidecar") "Sidecar state leaked"
        Assert-Contract ($env:BRIDRA_SERVER_PATH -eq "contract-server") "Server state leaked"

        $commands = @($global:BridraWindowsContractCalls | ForEach-Object { (@($_.name) + $_.arguments) -join " " })
        if ($scenario.failure) {
            Assert-Contract ($commands[-1] -eq $scenario.failure) "Commands continued after native failure"
            Assert-Contract ($caught.ToString().Contains("exit code 17")) "Native failure was obscured"
        }
        else {
            Assert-Contract (@($commands | Where-Object { $_ -eq "go test ./..." }).Count -eq 2) "Backend/SQL tests missing"
            Assert-Contract (@($commands | Where-Object { $_ -eq "fvm flutter test" }).Count -eq 2) "Flutter suite missing"
            Assert-Contract ($commands -contains "go vet ./...") "Native vet missing"
            Assert-Contract (@($commands | Where-Object { $_ -match '^go build -trimpath' }).Count -eq 2) "Native test backend build missing"
            Assert-Contract (@($commands | Where-Object { $_ -match 'doctor' }).Count -eq 1) "Windows doctor missing"
            Assert-Contract (@($commands | Where-Object { $_ -match '\^TestPublic' }).Count -eq 0) "Public API tests duplicated"
            Assert-Contract (($commands -contains "gofmt -l .") -eq (-not $scenario.delegated)) "Wrong formatting delegation"
            Assert-Contract (@($commands | Where-Object { $_ -eq "fvm flutter analyze" }).Count -eq $(if ($scenario.delegated) { 0 } else { 2 })) "Wrong analysis delegation"
            Assert-Contract (@($commands | Where-Object { $_ -match 'release check' }).Count -eq $(if ($scenario.delegated) { 0 } else { 1 })) "Wrong metadata delegation"
            Assert-Contract (@($commands | Where-Object { $_ -match 'generate.*--check' }).Count -eq $(if ($scenario.delegated) { 0 } else { 1 })) "Wrong generation delegation"
        }
        if ($scenario.timed) {
            $records = @([IO.File]::ReadAllLines($timings) | ForEach-Object { $_ | ConvertFrom-Json })
            $commandRecords = @($records | Where-Object { $_.kind -eq "command" })
            Assert-Contract ($commandRecords.Count -eq $commands.Count) "Missing command timing"
            for ($index = 0; $index -lt $commands.Count; $index++) {
                $record = $commandRecords[$index]
                Assert-Contract (((@($record.command) + $record.arguments) -join " ") -eq $commands[$index]) "Wrong timing identity"
                Assert-Contract ($record.directory -eq $global:BridraWindowsContractCalls[$index].directory) "Wrong timing directory"
                Assert-Contract ($record.seconds -ge 0) "Invalid elapsed time"
            }
            Assert-Contract ($records[-1].kind -eq "task") "Missing task timing"
            Assert-Contract ($records[-1].success -eq (-not [bool]$scenario.failure)) "Wrong task outcome"
            if ($scenario.failure) {
                Assert-Contract ($commandRecords[-1].exitCode -eq 17 -and -not $commandRecords[-1].success) "Missing failed-command timing"
            }
            Assert-Contract (Test-Path $summary) "Missing summary"
        }
        else {
            Assert-Contract (-not (Test-Path $timings)) "Timing should be opt-in"
            Assert-Contract (-not (Test-Path $summary)) "Untimed commands wrote a CI summary"
        }
    }
    $releaseScenarios = @(
        @{ name = "prepare"; parameters = @{ Task = "release-prepare"; Version = "0.1.1" }; expected = @("go run ./cmd/bridra release prepare --root .. 0.1.1", "fvm flutter pub get") },
        @{ name = "prepare-date"; parameters = @{ Task = "release-prepare"; Version = "0.1.1"; ReleaseDate = "2026-07-24" }; expected = @("go run ./cmd/bridra release prepare --root .. 0.1.1 --date 2026-07-24", "fvm flutter pub get") },
        @{ name = "prepare-failure"; parameters = @{ Task = "release-prepare"; Version = "0.1.1"; ReleaseDate = "2026-07-24" }; expected = @("go run ./cmd/bridra release prepare --root .. 0.1.1 --date 2026-07-24"); failure = "go run ./cmd/bridra release prepare --root .. 0.1.1 --date 2026-07-24" },
        @{ name = "check-final"; parameters = @{ Task = "release-check"; Version = "0.1.1"; Final = $true }; expected = @("go run ./cmd/bridra release check --root .. --version 0.1.1 --final") },
        @{ name = "check-date-rejected"; parameters = @{ Task = "release-check"; ReleaseDate = "2026-07-24" }; expected = @(); rejected = $true },
        @{ name = "missing-version"; parameters = @{ Task = "release-prepare"; ReleaseDate = "2026-07-24" }; expected = @(); rejected = $true },
        @{ name = "empty-date"; parameters = @{ Task = "release-prepare"; Version = "0.1.1"; ReleaseDate = "" }; expected = @(); rejected = $true }
    )
    foreach ($scenario in $releaseScenarios) {
        $global:BridraWindowsContractCalls = [Collections.Generic.List[object]]::new()
        $global:BridraWindowsContractFailure = $scenario.failure
        $env:BRIDRA_WINDOWS_TIMINGS_PATH = $null
        $caught = $null
        $location = (Get-Location).Path
        try {
            $parameters = $scenario.parameters
            & (Join-Path $fixture "tool/windows.ps1") @parameters
        }
        catch { $caught = $_ }
        $expectedFailure = [bool]$scenario.failure -or [bool]$scenario.rejected
        Assert-Contract (($null -ne $caught) -eq $expectedFailure) "$($scenario.name): wrong failure result: $caught"
        Assert-Contract ((Get-Location).Path -eq $location) "Release task did not restore working directory"
        $commands = @($global:BridraWindowsContractCalls | ForEach-Object { (@($_.name) + $_.arguments) -join " " })
        Assert-Contract (($commands -join "`n") -ceq ($scenario.expected -join "`n")) "$($scenario.name): wrong release commands: $commands"
        if ($scenario.failure) {
            Assert-Contract ($caught.ToString().Contains("exit code 17")) "Release failure was obscured"
        }
    }
    Write-Host "Windows verification/release contracts passed (12 scenarios)."
}
finally {
    foreach ($name in $savedEnvironment.Keys) {
        [Environment]::SetEnvironmentVariable($name, $savedEnvironment[$name])
    }
    Remove-Variable -Scope Global -Name BridraWindowsContractCalls, BridraWindowsContractFailure -ErrorAction SilentlyContinue
    Remove-Item -Recurse -Force $fixture -ErrorAction SilentlyContinue
}
