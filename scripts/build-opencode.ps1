$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path $PSScriptRoot -Parent
$version = '1.19.0'
$digest = '17108e9448c4fdad48eb8714c4ed6f5dfc9cf86c2eb8f2ce5d02043fef806eb5'
$tools = Join-Path $projectRoot ".tools/opencode-$version"
$archive = Join-Path $tools 'opencode-windows-x64.zip'
New-Item -ItemType Directory -Force $tools | Out-Null
function ArchiveHash([string]$path) {
    $stream = [System.IO.File]::OpenRead($path)
    $sha = [System.Security.Cryptography.SHA256]::Create()
    try { return [BitConverter]::ToString($sha.ComputeHash($stream)).Replace('-', '').ToLowerInvariant() }
    finally { $sha.Dispose(); $stream.Dispose() }
}
if (-not (Test-Path -LiteralPath $archive)) {
    Invoke-WebRequest -Uri "https://github.com/anomalyco/opencode/releases/download/v$version/opencode-windows-x64.zip" -OutFile ($archive + '.download')
    if ((ArchiveHash ($archive + '.download')) -ne $digest) { throw 'Official OpenCode archive SHA256 mismatch.' }
    Move-Item -LiteralPath ($archive + '.download') -Destination $archive
}
if ((ArchiveHash $archive) -ne $digest) { throw 'Cached OpenCode archive SHA256 mismatch.' }
$extracted = Join-Path $tools 'extracted'
Add-Type -AssemblyName System.IO.Compression.FileSystem
New-Item -ItemType Directory -Force $extracted | Out-Null
$zip = [System.IO.Compression.ZipFile]::OpenRead($archive)
try { $entry = $zip.GetEntry('opencode.exe'); if (-not $entry) { throw 'Official archive binary missing.' }; [System.IO.Compression.ZipFileExtensions]::ExtractToFile($entry, (Join-Path $extracted 'opencode.exe'), $true) }
finally { $zip.Dispose() }
$binary = Join-Path $extracted 'opencode.exe'
$binaryDir = Join-Path $projectRoot 'desktop/src-tauri/binaries'
New-Item -ItemType Directory -Force $binaryDir | Out-Null
Copy-Item -LiteralPath $binary -Destination (Join-Path $binaryDir 'moviedesign-opencode-x86_64-pc-windows-msvc.exe') -Force
Write-Output "OpenCode $version official archive verified: $digest"
