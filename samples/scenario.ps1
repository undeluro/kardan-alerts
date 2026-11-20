# Plays a scenario from samples\scenarios\<name>\ (Windows). Each file is
# posted to the source it is named after, so the source's parser applies.
#   .\samples\scenario.ps1 counterparty-outage
#   .\samples\scenario.ps1 recovery
#   .\samples\scenario.ps1 pricing-burst
param([string]$Name, [string]$Url = "http://localhost:8080")
$root = Join-Path $PSScriptRoot "scenarios"
if (-not $Name -or -not (Test-Path (Join-Path $root $Name))) {
    "usage: .\samples\scenario.ps1 <scenario>"
    "scenarios:"; Get-ChildItem $root -Directory | ForEach-Object { "  " + $_.Name }
    exit 1
}
Get-ChildItem (Join-Path $root $Name) -File | ForEach-Object {
    $src = $_.BaseName
    $path = if ($_.Extension -in ".json", ".xml") { "/ingest/$src" } else { "/ingest/$src/batch" }
    $body = [System.IO.File]::ReadAllBytes($_.FullName)
    $resp = Invoke-RestMethod -Method Post -Uri "$Url$path" -Body $body -ContentType "application/octet-stream"
    "{0,-22} -> accepted {1}" -f $src, $resp.accepted
}
