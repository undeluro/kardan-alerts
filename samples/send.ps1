# Sends the sample events through every source type (Windows):
#   HTTP webhooks -> alertmanager (metric breach), swift-gateway (ISO 20022 pacs.002 XML)
#   Kafka topics  -> workflow.events, trade.events              (source "broker-events")
#   Log files     -> samples\logs\pricing-engine.log, fix-gateway.log
#
#   .\samples\send.ps1
#   .\samples\send.ps1 -Url http://host:8080
param([string]$Url = "http://localhost:8080")
$root = Split-Path $PSScriptRoot -Parent
Set-Location $root

function Send-Http([string]$Source, [string]$File, [string]$ContentType) {
    $body = [System.IO.File]::ReadAllBytes((Join-Path $root "samples\events\$File"))
    $resp = Invoke-RestMethod -Method Post -Uri "$Url/ingest/$Source" -ContentType $ContentType -Body $body
    "{0,-44}accepted {1}" -f "HTTP  $Source $File", $resp.accepted
}

Send-Http alertmanager  metric-breach.json    "application/json"
Send-Http swift-gateway pacs002-rejected.xml  "application/xml"

foreach ($topic in "workflow.events", "trade.events") {
    $file = Join-Path $root "samples\kafka\$topic.ndjson"
    Get-Content $file | docker compose exec -T redpanda rpk topic produce $topic | Out-Null
    if ($LASTEXITCODE -eq 0) {
        "{0,-44}produced {1} record(s)" -f "Kafka $topic", (Get-Content $file).Count
    } else {
        $resp = Invoke-RestMethod -Method Post -Uri "$Url/ingest/broker-events/batch" -Body ([System.IO.File]::ReadAllBytes($file))
        "{0,-44}accepted {1} (via HTTP)" -f "Kafka $topic", $resp.accepted
    }
}

$logs = Join-Path $root "samples\logs"
New-Item -ItemType Directory -Force $logs | Out-Null
foreach ($pair in @(@("pricing.log", "pricing-engine.log"), @("fix.log", "fix-gateway.log"))) {
    $lines = [System.IO.File]::ReadAllText((Join-Path $root "samples\lines\$($pair[0])"))
    [System.IO.File]::AppendAllText((Join-Path $logs $pair[1]), $lines.Replace("`r`n", "`n"))
    "{0,-44}appended {1} lines" -f "File  samples\logs\$($pair[1])", ($lines.Trim() -split "`n").Count
}

""
"Done. Alerts: $Url   Emails: http://localhost:8025   Schemas: $Url/schemas"
