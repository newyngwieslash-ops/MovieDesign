$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path $PSScriptRoot -Parent
$version = '9.0.2'
$digest = '60f467265b1e312373dbcd92200c2618a74850f98d3d078e94296bb3fa2047ba'
$cache = Join-Path $projectRoot ".tools/ffmpeg-$version"
$archive = Join-Path $cache 'archive.zip.download'
New-Item -ItemType Directory -Force $cache | Out-Null
if (-not (Test-Path -LiteralPath $archive)) {
    Invoke-WebRequest -Uri "https://github.com/GyanD/codexffmpeg/releases/download/$version/ffmpeg-$version-essentials_build.zip" -OutFile $archive
}
$stream = [IO.File]::OpenRead($archive)
$sha = [Security.Cryptography.SHA256]::Create()
try { $actual = [BitConverter]::ToString($sha.ComputeHash($stream)).Replace('-', '').ToLowerInvariant() }
finally { $sha.Dispose(); $stream.Dispose() }
if ($actual -ne $digest) { throw 'Public FFmpeg archive SHA256 mismatch.' }
Add-Type -AssemblyName System.IO.Compression.FileSystem
$target = Join-Path $projectRoot 'desktop/src-tauri/binaries/media'
New-Item -ItemType Directory -Force $target | Out-Null
$zip = [IO.Compression.ZipFile]::OpenRead($archive)
try {
    foreach ($pair in @(@('bin/ffprobe.exe','ffprobe.exe'),@('LICENSE','LICENSE'),@('README.txt','README.txt'))) {
        $entry = $zip.GetEntry("ffmpeg-$version-essentials_build/$($pair[0])")
        if (-not $entry) { throw 'Pinned archive entry missing.' }
        [IO.Compression.ZipFileExtensions]::ExtractToFile($entry, (Join-Path $target $pair[1]), $true)
    }
} finally { $zip.Dispose() }
Write-Output "ffprobe $version public Gyan essentials archive verified: $digest; GPLv3 license and build/source details bundled."
