param(
    [ValidateSet('auto','web','contracts','cloud','residual','snapshot','negative','env-check')]
    [string]$Stage = 'auto',
    [string]$Repo = (Resolve-Path (Join-Path $PSScriptRoot '../../../../..')).Path,
    [string]$OutputRoot = (Join-Path ([System.IO.Path]::GetTempPath()) ('lbt148-audit-' + [guid]::NewGuid().ToString('N'))),
    [string]$PythonExecutable = 'python',
    [string]$NodeBin = '',
    [string]$PnpmExecutable = 'pnpm',
    [string]$ValidatorDir = ''
)
$ErrorActionPreference = 'Stop'
$env:LABBIT_EVIDENCE_REPO = (Resolve-Path -LiteralPath $Repo).Path
$env:LABBIT_EVIDENCE_OUT = $OutputRoot
if ($NodeBin) { $env:LABBIT_EVIDENCE_NODE_BIN = $NodeBin }
$env:LABBIT_EVIDENCE_PNPM = $PnpmExecutable
if ($ValidatorDir) { $env:LABBIT_EVIDENCE_VALIDATORS = $ValidatorDir }
$runner = if ($Stage -eq 'negative') { 'check_pre_fix.py' } elseif ($Stage -eq 'env-check') { 'verify_runner_env.py' } else { 'validate.py' }
& $PythonExecutable (Join-Path $PSScriptRoot $runner) $Stage
if ($LASTEXITCODE -ne 0) { throw "LBT-148 $Stage validation failed; raw logs: $OutputRoot" }
Write-Output "LBT-148 $Stage validation succeeded; raw logs: $OutputRoot"
