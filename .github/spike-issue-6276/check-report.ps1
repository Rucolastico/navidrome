# Spike for issue #6276. Checks the Ginkgo JSON reports of the issue-6276 specs against the
# expected per-spec outcome for the given mode, and writes a table to the job summary.
#   baseline: original InPath. The Windows separator specs must FAIL, the control specs must PASS.
#   fix:      patched InPath. Every spec must PASS.
param(
    [Parameter(Mandatory)][ValidateSet('baseline', 'fix')][string]$Mode,
    [Parameter(Mandatory)][string]$TestOutcome,
    [Parameter(Mandatory)][string[]]$Reports
)
$ErrorActionPreference = 'Stop'

# Specs that fail on Windows with the original InPath (folder path built with '\')
$windowsBug = @(
    'returns true if folder is in PlaylistsPath',
    'nested folder, exact pattern',
    'nested folder, ** pattern',
    'nested folder, second item of a list',
    'backslash pattern',
    'backslash ** pattern',
    'nested folder',
    'root and a nested ** pattern',
    'backslash nested folder (issue config)'
)
# Controls: pass with both the original and the patched InPath
$controls = @(
    'top-level folder',
    "root folder, '.' in a list",
    'sibling folder is excluded',
    'child folder is excluded by an exact pattern',
    'root folder is excluded by a nested pattern',
    'backslash pattern, sibling folder is excluded',
    'empty (default) imports everything',
    'non-matching pattern imports nothing'
)

$expected = @{}
foreach ($n in $windowsBug) { $expected[$n] = if ($Mode -eq 'baseline') { 'failed' } else { 'passed' } }
foreach ($n in $controls) { $expected[$n] = 'passed' }

$actual = @{}
foreach ($r in $Reports) {
    if (-not (Test-Path $r)) { throw "Missing Ginkgo report: $r (did the tests build and run?)" }
    foreach ($suite in (Get-Content $r -Raw | ConvertFrom-Json)) {
        foreach ($spec in $suite.SpecReports) {
            if ($spec.LeafNodeType -ne 'It' -or $spec.State -eq 'skipped') { continue }
            $name = $spec.LeafNodeText
            if ($actual.ContainsKey($name)) { throw "Duplicate spec name in reports: $name" }
            $actual[$name] = $spec.State
        }
    }
}

$problems = @()
$rows = @('| Spec | Expected | Actual | |', '|---|---|---|---|')
foreach ($name in ($expected.Keys | Sort-Object)) {
    $got = if ($actual.ContainsKey($name)) { $actual[$name] } else { 'not run' }
    $ok = $got -eq $expected[$name]
    if (-not $ok) { $problems += "$name : expected $($expected[$name]), got $got" }
    $rows += "| $name | $($expected[$name]) | $got | $(if ($ok) { 'OK' } else { 'MISMATCH' }) |"
}
foreach ($name in $actual.Keys) {
    if (-not $expected.ContainsKey($name)) { $problems += "Unexpected spec ran: $name ($($actual[$name]))" }
}

$wantOutcome = if ($Mode -eq 'baseline') { 'failure' } else { 'success' }
if ($TestOutcome -ne $wantOutcome) { $problems += "Test step outcome: expected $wantOutcome, got $TestOutcome" }

$summary = @("## Issue #6276 - $Mode on $([System.Environment]::OSVersion.VersionString)", '',
    "Test step outcome: **$TestOutcome** (expected $wantOutcome)", '') + $rows
if ($problems.Count -eq 0) {
    $summary += '', "**Result: every spec matched the expected $Mode outcome.**"
} else {
    $summary += '', '**Result: MISMATCH**', ''
    $summary += @($problems | ForEach-Object { "- $_" })
}
$summary -join "`n" | Tee-Object -Append -FilePath $env:GITHUB_STEP_SUMMARY
if ($problems.Count -gt 0) { exit 1 }
