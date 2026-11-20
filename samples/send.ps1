# Sends the Appendix A sample events to a running Kardan instance (Windows).
#   .\samples\send.ps1
#   .\samples\send.ps1 -Url http://host:8080
param([string]$Url = "http://localhost:8080")
$dir = Join-Path $PSScriptRoot "events"

function Send-Event([string]$Source, [string]$File, [string]$Suffix = "") {
    $body = [System.IO.File]::ReadAllBytes((Join-Path $dir $File))
    $resp = Invoke-RestMethod -Method Post -Uri "$Url/ingest/$Source$Suffix" -ContentType "application/json" -Body $body
    "{0,-28} -> accepted {1}" -f $File, $resp.accepted
}

Send-Event workflow-engine workflow-failed.json
Send-Event alertmanager    metric-breach.json
Send-Event trade-gateway   trade-rejected.json
Send-Event pricing-logs    pricing-warn.ndjson "/batch"

"Done. Open $Url (alerts) and http://localhost:8025 (Mailpit)."
