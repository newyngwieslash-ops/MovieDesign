param(
    [string]$OutputDir = ""
)
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path $PSScriptRoot -Parent
if ([string]::IsNullOrWhiteSpace($OutputDir)) {
    $OutputDir = Join-Path $projectRoot '.runtime/acceptance-lineage'
}
New-Item -ItemType Directory -Force -Path $OutputDir | Out-Null

function Resolve-Sqlite3Cli {
    $cmd = Get-Command sqlite3 -ErrorAction SilentlyContinue
    if ($null -ne $cmd -and (Test-Path $cmd.Source)) {
        return $cmd.Source
    }
    $toolsDir = Join-Path $projectRoot '.tools/sqlite'
    $localExe = Join-Path $toolsDir 'sqlite3.exe'
    if (Test-Path $localExe) {
        return $localExe
    }
    New-Item -ItemType Directory -Force -Path $toolsDir | Out-Null
    $zipPath = Join-Path $toolsDir 'sqlite-tools-win-x64-3460100.zip'
    $url = 'https://www.sqlite.org/2024/sqlite-tools-win-x64-3460100.zip'
    Write-Host "Downloading official SQLite CLI tools from $url ..."
    Invoke-WebRequest -Uri $url -OutFile $zipPath -UseBasicParsing
    Expand-Archive -Path $zipPath -DestinationPath $toolsDir -Force
    $found = Get-ChildItem -Path $toolsDir -Filter 'sqlite3.exe' -Recurse | Select-Object -First 1
    if ($null -eq $found) {
        throw 'Failed to provision official sqlite3.exe CLI.'
    }
    if ($found.FullName -ne $localExe) {
        Copy-Item -Force $found.FullName $localExe
    }
    return $localExe
}

$sqlite3 = Resolve-Sqlite3Cli
$sqliteVersion = (& $sqlite3 --version | Select-Object -First 1).Trim()

& (Join-Path $PSScriptRoot 'build-media-tools.ps1')
$env:MOVIEDESIGN_FFPROBE_TEST_BINARY = Join-Path $projectRoot 'desktop/src-tauri/binaries/media/ffprobe.exe'
$env:MOVIEDESIGN_ACCEPTANCE_EXPORT_DIR = $OutputDir

Push-Location (Join-Path $projectRoot 'gateway')
try {
    & go test ./internal/pipeline -run '^TestAcceptanceEndToEndNarratedVideoAndLineageExport$' -count=1 -v
    if ($LASTEXITCODE -ne 0) {
        throw 'TestAcceptanceEndToEndNarratedVideoAndLineageExport failed.'
    }
} finally {
    Pop-Location
    Remove-Item Env:\MOVIEDESIGN_ACCEPTANCE_EXPORT_DIR -ErrorAction SilentlyContinue
}

$dbPath = Join-Path $OutputDir 'acceptance-lineage.sqlite'
$manifestPath = Join-Path $OutputDir 'acceptance-lineage-manifest.json'
$dumpPath = Join-Path $OutputDir 'acceptance-dump.sql'

if (-not (Test-Path $dbPath) -or -not (Test-Path $manifestPath)) {
    throw "Scoped acceptance DB or manifest missing under $OutputDir"
}

$manifest = Get-Content -Raw -Path $manifestPath | ConvertFrom-Json
$prevOutputEncoding = [Console]::OutputEncoding
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
try {
    $dumpLines = & $sqlite3 $dbPath '.dump'
    if ($LASTEXITCODE -ne 0) {
        throw 'sqlite3 .dump failed.'
    }
} finally {
    [Console]::OutputEncoding = $prevOutputEncoding
}
$dumpText = ($dumpLines -join "`n")
Set-Content -Path $dumpPath -Value $dumpText -Encoding UTF8

# 1. Verify zero credential, header, or cross-project leakage in both SQLite dump and tables
foreach ($marker in $manifest.redactedMarkers) {
    if ($dumpText.Contains([string]$marker)) {
        throw "Forbidden marker '$marker' leaked into sqlite3 .dump output!"
    }
}

$leakQuery = @"
SELECT
  (SELECT COUNT(*) FROM projects WHERE id <> '$($manifest.projectId)') || '|' ||
  (SELECT COUNT(*) FROM provider_configs) || '|' ||
  (SELECT COUNT(*) FROM llm_settings) || '|' ||
  (SELECT COUNT(*) FROM assets WHERE id = '$($manifest.excludedAssetId)');
"@
$leakCounts = (& $sqlite3 $dbPath $leakQuery).Trim()
if ($leakCounts -ne '0|0|0|0') {
    throw "Scoped export isolation check failed: expected 0|0|0|0, got $leakCounts"
}

# 2. Verify full multi-table SQL lineage join (storyboard Version -> prompt Version -> prompt_contract -> generation_inputs/task -> task_output -> delivered Version -> Asset)
$chainQuery = @"
SELECT
  sr.document_version_id || '|' ||
  pr.document_version_id || '|' ||
  pc.id || '|' ||
  pc.status || '|' ||
  t.id || '|' ||
  t.status || '|' ||
  to1.task_output_key || '|' ||
  to1.state || '|' ||
  v1.id || '|' ||
  v1.resource_id || '|' ||
  a1.id || '|' ||
  a1.width || 'x' || a1.height || '|' ||
  a1.duration_ms || '|' ||
  v2.id || '|' ||
  v2.source_version_id || '|' ||
  a2.id
FROM storyboard_revisions sr
JOIN approvals ap_sb ON ap_sb.pipeline_id = sr.pipeline_id AND ap_sb.gate = 'storyboard' AND ap_sb.revision = sr.revision AND ap_sb.valid = 1
JOIN prompt_revisions pr ON pr.pipeline_id = sr.pipeline_id AND pr.source_version_id = sr.document_version_id
JOIN prompt_contracts pc ON pc.pipeline_id = sr.pipeline_id AND pc.document_version_id = pr.document_version_id
JOIN tasks t ON t.id = pc.task_id
JOIN task_outputs to1 ON to1.task_id = t.id AND to1.attempt = 1
JOIN versions v1 ON v1.id = to1.version_id
JOIN assets a1 ON a1.id = v1.asset_id
JOIN versions v2 ON v2.resource_id = v1.resource_id AND v2.source_version_id = v1.id
JOIN assets a2 ON a2.id = v2.asset_id
WHERE sr.pipeline_id = '$($manifest.pipelineId)' AND sr.revision = 1;
"@
$chainResult = (& $sqlite3 $dbPath $chainQuery).Trim()
$expectedChain = @(
    $manifest.storyboardVersionId,
    $manifest.promptVersionId,
    $manifest.contractId,
    'submitted',
    $manifest.taskId,
    'completed',
    $manifest.taskOutputKey,
    'delivered',
    $manifest.videoVersionV1Id,
    $manifest.videoResourceId,
    $manifest.videoAssetV1Id,
    '1280x720',
    '15000',
    $manifest.videoVersionV2Id,
    $manifest.videoVersionV1Id,
    $manifest.videoAssetV2Id
) -join '|'

if ($chainResult -ne $expectedChain) {
    throw "Full SQL lineage join mismatch!`nExpected: $expectedChain`nActual:   $chainResult"
}

# 3. Verify explicit version_dependencies edges (not relying solely on versions.source_version_id)
$depQuery = @"
SELECT kind || ':' || source_version_id
FROM version_dependencies
WHERE version_id IN ('$($manifest.videoVersionV1Id)', '$($manifest.videoVersionV2Id)')
ORDER BY kind ASC;
"@
$depLines = @(& $sqlite3 $dbPath $depQuery | ForEach-Object { $_.Trim() } | Where-Object { $_ -ne '' })
$requiredDeps = @(
    "prompt_contract:$($manifest.promptVersionId)",
    "reference:0:$($manifest.orderedReferenceVersionIds[0])",
    "reference:1:$($manifest.orderedReferenceVersionIds[1])",
    "storyboard_source:$($manifest.storyboardVersionId)"
)
foreach ($reqDep in $requiredDeps) {
    if ($depLines -notcontains $reqDep) {
        throw "Missing required version_dependencies edge '$reqDep' in: $($depLines -join ', ')"
    }
}

# 4. Verify ordered multi-reference restoration from generation_inputs
$refQuery = @"
SELECT ordinal || ':' || asset_id
FROM generation_inputs
WHERE task_id = '$($manifest.taskId)'
ORDER BY ordinal ASC;
"@
$refLines = @(& $sqlite3 $dbPath $refQuery | ForEach-Object { $_.Trim() } | Where-Object { $_ -ne '' })
$expectedRefs = @(
    "0:$($manifest.orderedReferenceAssetIds[0])",
    "1:$($manifest.orderedReferenceAssetIds[1])"
)
if (($refLines -join '|') -ne ($expectedRefs -join '|')) {
    throw "Ordered multi-reference restoration mismatch: expected $($expectedRefs -join '|'), got $($refLines -join '|')"
}

# 5. Confirm sqlite3 .dump contains INSERT statements for every required lineage table
$requiredTablesInDump = @(
    'projects',
    'compositions',
    'resources',
    'versions',
    'assets',
    'storyboard_revisions',
    'approvals',
    'prompt_revisions',
    'prompt_contracts',
    'tasks',
    'generation_inputs',
    'task_outputs',
    'version_dependencies'
)
foreach ($tbl in $requiredTablesInDump) {
    if ($dumpText -notmatch "INSERT INTO `"$tbl`"|INSERT INTO $tbl") {
        throw "sqlite3 .dump missing INSERT statements for table '$tbl'"
    }
}

$report = [ordered]@{
    ok                        = $true
    sqliteCli                 = $sqlite3
    sqliteVersion             = $sqliteVersion
    databasePath              = $dbPath
    dumpPath                  = $dumpPath
    dumpSha256                = (Get-FileHash -Path $dumpPath -Algorithm SHA256).Hash.ToLowerInvariant()
    projectId                 = $manifest.projectId
    pipelineId                = $manifest.pipelineId
    storyboardVersionId       = $manifest.storyboardVersionId
    promptVersionId           = $manifest.promptVersionId
    contractId                = $manifest.contractId
    taskId                    = $manifest.taskId
    taskOutputKey             = $manifest.taskOutputKey
    videoResourceId           = $manifest.videoResourceId
    videoVersionV1Id          = $manifest.videoVersionV1Id
    videoVersionV2Id          = $manifest.videoVersionV2Id
    videoAssetV1Id            = $manifest.videoAssetV1Id
    videoAssetV2Id            = $manifest.videoAssetV2Id
    orderedReferenceAssetIds  = $manifest.orderedReferenceAssetIds
    verifiedDependencyEdges   = $depLines
    isolatedOtherProjects     = $true
    redactedSecretsAndHeaders = $true
}
$reportPath = Join-Path $OutputDir 'verify-lineage-report.json'
$report | ConvertTo-Json -Depth 6 | Set-Content -Path $reportPath -Encoding UTF8
Write-Host "SQL lineage verification passed: $reportPath"
