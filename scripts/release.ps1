param(
    [switch]$DryRun,
    [string]$Version = '',
    [string]$OutputDir = ''
)

$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path $PSScriptRoot -Parent

$tauriConfPath = Join-Path $projectRoot 'desktop/src-tauri/tauri.conf.json'
$policyPath = Join-Path $projectRoot 'updates/stable/policy.json'
$thirdPartyPath = Join-Path $projectRoot 'docs/third-party.md'

if (-not (Test-Path -LiteralPath $tauriConfPath)) { throw "Missing $tauriConfPath" }
if (-not (Test-Path -LiteralPath $policyPath)) { throw "Missing $policyPath" }
if (-not (Test-Path -LiteralPath $thirdPartyPath)) { throw "Missing $thirdPartyPath" }

$tauriConf = [IO.File]::ReadAllText($tauriConfPath) | ConvertFrom-Json
$policy = [IO.File]::ReadAllText($policyPath) | ConvertFrom-Json

$resolvedVersion = if ($Version.Trim()) { $Version.Trim().TrimStart('v') } else { [string]$tauriConf.version }
if ($resolvedVersion -notmatch '^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$') {
    throw "Invalid release version: $resolvedVersion"
}

$expectedManifestEndpoint = 'https://github.com/newyngwieslash-ops/MovieDesign/releases/latest/download/latest.json'
$expectedPolicyUrl = 'https://raw.githubusercontent.com/newyngwieslash-ops/MovieDesign/main/updates/stable/policy.json'
$expectedReleasesUrl = 'https://github.com/newyngwieslash-ops/MovieDesign/releases/latest'

if (-not $tauriConf.bundle.createUpdaterArtifacts) {
    throw 'tauri.conf.json must enable bundle.createUpdaterArtifacts = true.'
}
if (-not $tauriConf.plugins.updater.pubkey -or -not ([string]$tauriConf.plugins.updater.pubkey).Trim()) {
    throw 'tauri.conf.json must include a non-empty plugins.updater.pubkey.'
}
if (($tauriConf.plugins.updater.endpoints -notcontains $expectedManifestEndpoint)) {
    throw "tauri.conf.json plugins.updater.endpoints must contain $expectedManifestEndpoint"
}
if ([int]$policy.schema_version -ne 1) {
    throw 'updates/stable/policy.json must have schema_version = 1.'
}
if ([string]$policy.manual_download_url -ne $expectedReleasesUrl) {
    throw "updates/stable/policy.json manual_download_url must be $expectedReleasesUrl"
}

Write-Host "==> Building media tools, gateway, and opencode sidecars..."
& (Join-Path $PSScriptRoot 'build-media-tools.ps1')
& (Join-Path $PSScriptRoot 'build-gateway.ps1')
& (Join-Path $PSScriptRoot 'build-opencode.ps1')

# Preserve baseline line endings on generated media_prompt.go if unchanged in content
Push-Location $projectRoot
try {
    $diffMedia = (& git diff --ignore-cr-at-eol --name-only -- gateway/internal/agent/media_prompt.go)
    if (-not $diffMedia) {
        & git checkout -- gateway/internal/agent/media_prompt.go 2>$null
    }
} finally {
    Pop-Location
}

$targetTriple = (& rustc --print host-tuple).Trim()
$binDir = Join-Path $projectRoot 'desktop/src-tauri/binaries'
$gatewayBin = Join-Path $binDir "open-design-gateway-$targetTriple.exe"
$opencodeBin = Join-Path $binDir "moviedesign-opencode-$targetTriple.exe"
$ffprobeBin = Join-Path $binDir 'media/ffprobe.exe'
$ffprobeLicense = Join-Path $binDir 'media/LICENSE'

foreach ($requiredBin in @($gatewayBin, $opencodeBin, $ffprobeBin, $ffprobeLicense)) {
    if (-not (Test-Path -LiteralPath $requiredBin)) {
        throw "Required release binary missing: $requiredBin"
    }
}

$hasPrivateKey = (-not [string]::IsNullOrWhiteSpace($env:TAURI_SIGNING_PRIVATE_KEY)) -or
                 (-not [string]::IsNullOrWhiteSpace($env:TAURI_SIGNING_PRIVATE_KEY_PATH))

if (-not $DryRun -and -not $hasPrivateKey) {
    throw 'TAURI_SIGNING_PRIVATE_KEY (or TAURI_SIGNING_PRIVATE_KEY_PATH) is not configured. Refusing to produce unsigned or fake-signed auto-update artifacts.'
}

if (-not $OutputDir.Trim()) {
    $OutputDir = if ($DryRun) {
        Join-Path $projectRoot '.runtime/release-dry-run'
    } else {
        Join-Path $projectRoot ".runtime/release-v$resolvedVersion"
    }
}
New-Item -ItemType Directory -Force -Path $OutputDir | Out-Null

$releaseNotesPath = Join-Path $OutputDir 'RELEASE-NOTES.md'
$sha256Path = Join-Path $OutputDir 'SHA256SUMS.txt'
$policyCopyPath = Join-Path $OutputDir 'policy.json'
$thirdPartyCopyPath = Join-Path $OutputDir 'third-party.md'

Copy-Item -LiteralPath $policyPath -Destination $policyCopyPath -Force
Copy-Item -LiteralPath $thirdPartyPath -Destination $thirdPartyCopyPath -Force

if ($DryRun) {
    Write-Host "==> Running frontend build in dry-run mode..."
    Push-Location (Join-Path $projectRoot 'desktop')
    try {
        & pnpm build
        if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed during release dry-run.' }
    } finally {
        Pop-Location
    }

    $dryRunMeta = [ordered]@{
        mode                    = 'dry-run'
        version                 = $resolvedVersion
        repository              = 'newyngwieslash-ops/MovieDesign'
        platform                = 'win32'
        rustTarget              = $targetTriple
        policyUrl               = $expectedPolicyUrl
        manifestUrl             = $expectedManifestEndpoint
        releasesUrl             = $expectedReleasesUrl
        signingKeyConfigured    = [bool]$hasPrivateKey
        signedInstallerProduced = $false
        note                    = 'Dry-run verified sidecars, frontend bundle, policy.json, third-party notices, and signing guard without generating a fake signature.'
        generatedAt             = (Get-Date).ToUniversalTime().ToString('o')
    }
    $dryRunMetaPath = Join-Path $OutputDir 'release-dry-run.json'
    [IO.File]::WriteAllText($dryRunMetaPath, ($dryRunMeta | ConvertTo-Json -Depth 6) + "`n", [Text.UTF8Encoding]::new($false))

    $notesLines = @(
        "# MovieDesign v$resolvedVersion (Dry-Run Verification)",
        "",
        "- **Repository**: newyngwieslash-ops/MovieDesign",
        "- **Target Platform**: Windows x64 ($targetTriple / policy platform win32)",
        "- **Update Policy URL**: $expectedPolicyUrl",
        "- **Signed Manifest URL**: $expectedManifestEndpoint",
        "- **Signing Key Configured**: $hasPrivateKey (UNEXECUTED for real signed installer when private key is absent)",
        "- **Third-Party Licenses**: Included in third-party.md"
    )
    [IO.File]::WriteAllText($releaseNotesPath, ($notesLines -join "`n") + "`n", [Text.UTF8Encoding]::new($false))

    $hashLines = @()
    foreach ($fileToHash in @($gatewayBin, $opencodeBin, $ffprobeBin, $ffprobeLicense, $policyCopyPath, $thirdPartyCopyPath, $dryRunMetaPath, $releaseNotesPath)) {
        $h = (Get-FileHash -LiteralPath $fileToHash -Algorithm SHA256).Hash.ToLowerInvariant()
        $leaf = Split-Path $fileToHash -Leaf
        $hashLines += "$h  $leaf"
    }
    [IO.File]::WriteAllText($sha256Path, ($hashLines -join "`n") + "`n", [Text.UTF8Encoding]::new($false))
    Write-Host "==> Release dry-run succeeded. Output written to $OutputDir"
    exit 0
}

Write-Host "==> Building signed Tauri NSIS installer..."
Push-Location (Join-Path $projectRoot 'desktop')
try {
    & pnpm tauri build --bundles nsis
    if ($LASTEXITCODE -ne 0) { throw 'Tauri signed release build failed.' }
} finally {
    Pop-Location
}

$nsisDir = Join-Path $projectRoot 'desktop/src-tauri/target/release/bundle/nsis'
$installer = Get-ChildItem -LiteralPath $nsisDir -Filter '*-setup.exe' | Sort-Object LastWriteTime -Descending | Select-Object -First 1
if (-not $installer) { throw "Signed NSIS installer not found in $nsisDir" }
$sigFile = "$($installer.FullName).sig"
if (-not (Test-Path -LiteralPath $sigFile)) {
    throw "Signature file missing: $sigFile. Refusing unsigned release."
}

$installerDest = Join-Path $OutputDir $installer.Name
$sigDest = Join-Path $OutputDir "$($installer.Name).sig"
Copy-Item -LiteralPath $installer.FullName -Destination $installerDest -Force
Copy-Item -LiteralPath $sigFile -Destination $sigDest -Force

$signatureContent = ([IO.File]::ReadAllText($sigFile)).Trim()
$downloadUrl = "https://github.com/newyngwieslash-ops/MovieDesign/releases/download/v$resolvedVersion/$($installer.Name)"
$latestManifest = [ordered]@{
    version   = $resolvedVersion
    notes     = "MovieDesign v$resolvedVersion signed Windows x64 release."
    pub_date  = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
    platforms = [ordered]@{
        'windows-x86_64'      = [ordered]@{
            url       = $downloadUrl
            signature = $signatureContent
        }
        'windows-x86_64-nsis' = [ordered]@{
            url       = $downloadUrl
            signature = $signatureContent
        }
    }
}
$latestJsonPath = Join-Path $OutputDir 'latest.json'
[IO.File]::WriteAllText($latestJsonPath, ($latestManifest | ConvertTo-Json -Depth 6) + "`n", [Text.UTF8Encoding]::new($false))

$notesLines = @(
    "# MovieDesign v$resolvedVersion",
    "",
    "- **Repository**: newyngwieslash-ops/MovieDesign",
    "- **Platform**: Windows x64 ($targetTriple, policy platform win32)",
    "- **Installer**: $($installer.Name)",
    "- **Signature**: $($installer.Name).sig",
    "- **Static Manifest**: latest.json",
    "- **Update Policy**: $expectedPolicyUrl",
    "- **Third-Party Licenses**: See third-party.md"
)
[IO.File]::WriteAllText($releaseNotesPath, ($notesLines -join "`n") + "`n", [Text.UTF8Encoding]::new($false))

$hashLines = @()
foreach ($fileToHash in @($installerDest, $sigDest, $latestJsonPath, $policyCopyPath, $thirdPartyCopyPath, $releaseNotesPath)) {
    $h = (Get-FileHash -LiteralPath $fileToHash -Algorithm SHA256).Hash.ToLowerInvariant()
    $leaf = Split-Path $fileToHash -Leaf
    $hashLines += "$h  $leaf"
}
[IO.File]::WriteAllText($sha256Path, ($hashLines -join "`n") + "`n", [Text.UTF8Encoding]::new($false))
Write-Host "==> Signed release artifacts generated in $OutputDir"
