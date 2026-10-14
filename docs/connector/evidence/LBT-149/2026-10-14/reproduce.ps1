param(
    [ValidateSet('auto','web','contracts','live','residual')][string]$Stage = 'auto',
    [string]$Repo = (Resolve-Path (Join-Path $PSScriptRoot '../../../../..')).Path,
    [string]$OutputRoot = (Join-Path ([System.IO.Path]::GetTempPath()) ('lbt149-' + [guid]::NewGuid().ToString('N'))),
    [string]$PythonExecutable = 'python',
    [string]$NodeBin = '',
    [string]$PnpmExecutable = 'pnpm',
    [string]$ValidatorDir = ''
)
$ErrorActionPreference = 'Stop'
$env:LABBIT_EVIDENCE_REPO = (Resolve-Path -LiteralPath $Repo).Path
$env:LABBIT_EVIDENCE_OUT = $OutputRoot
$env:LABBIT_EVIDENCE_PNPM = $PnpmExecutable
if ($NodeBin) { $env:LABBIT_EVIDENCE_NODE_BIN = $NodeBin }
if ($ValidatorDir) { $env:LABBIT_EVIDENCE_VALIDATORS = $ValidatorDir }
& $PythonExecutable (Join-Path $PSScriptRoot 'validate.py') $Stage
if ($LASTEXITCODE -ne 0) { throw "LBT-149 $Stage failed; raw logs: $OutputRoot" }
Write-Output "LBT-149 $Stage PASS; raw logs: $OutputRoot"
