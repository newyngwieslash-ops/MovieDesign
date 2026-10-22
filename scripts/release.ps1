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

$resolvedVersion = if ($Version.Trim()) { $Version.Trim() -creplace '^v', '' } else { [string]$tauriConf.version }
$identifier = '(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)'
if ($resolvedVersion -cnotmatch "^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-$identifier(\.$identifier)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$") {
    throw "Invalid release version: $resolvedVersion"
}
$npmPackage = [IO.File]::ReadAllText((Join-Path $projectRoot 'desktop/package.json')) | ConvertFrom-Json
$cargoPackage = [regex]::Match([IO.File]::ReadAllText((Join-Path $projectRoot 'desktop/src-tauri/Cargo.toml')), '(?ms)^\[package\]\s*\r?\n(.*?)(?=^\[|\z)').Groups[1].Value
$cargoVersion = [regex]::Match($cargoPackage, '(?m)^version\s*=\s*"([^"]+)"').Groups[1].Value
$cargoLock = [regex]::Match([IO.File]::ReadAllText((Join-Path $projectRoot 'desktop/src-tauri/Cargo.lock')), '(?m)^name = "moviedesign"\r?\nversion = "([^"]+)"').Groups[1].Value
foreach ($source in @(@('Tauri', [string]$tauriConf.version), @('npm', [string]$npmPackage.version), @('Cargo', $cargoVersion), @('Cargo.lock', $cargoLock))) {
    if ($source[1] -cne $resolvedVersion) { throw "Release version mismatch: $($source[0]) must equal $resolvedVersion." }
}
if ($env:GITHUB_REF -clike 'refs/tags/*' -and $env:GITHUB_REF -cne "refs/tags/v$resolvedVersion") {
    throw "Release tag mismatch: expected refs/tags/v$resolvedVersion."
}

function Read-SigningEnvelope([string]$Value, [string]$Kind) {
    try {
        $raw = [Text.UTF8Encoding]::new($false, $true).GetString([Convert]::FromBase64String($Value.Trim()))
        $lines = $raw.Trim() -split '\r?\n'
        if ($lines.Count -ne 2 -or $lines[0] -notmatch '^untrusted comment:') { throw 'invalid envelope' }
        $packet = [Convert]::FromBase64String($lines[1])
        $length = if ($Kind -eq 'public') { 42 } else { 158 }
        if ($packet.Length -ne $length -or $packet[0] -ne 69 -or $packet[1] -ne 100) { throw 'invalid packet' }
        return ,$packet
    } catch { throw "Invalid $Kind key format." }
}
$publicPacket = Read-SigningEnvelope ([string]$tauriConf.plugins.updater.pubkey) 'public'
# This public key is the checked-in updater unit-test vector, never a production identity.
$testPublicPacket = 'RWTX5qZvdPSepDGWg2bz8n6ArQnBMwgQK1geVosorcbLzcunD+KJ+cYe'
if (-not $DryRun -and [Convert]::ToBase64String($publicPacket) -ceq $testPublicPacket) {
    throw 'Production release refuses the public test vector; configure a formal updater public key.'
}
$hasPrivateKey = (-not [string]::IsNullOrWhiteSpace($env:TAURI_SIGNING_PRIVATE_KEY)) -or
                 (-not [string]::IsNullOrWhiteSpace($env:TAURI_SIGNING_PRIVATE_KEY_PATH))
if (-not $DryRun -and -not $hasPrivateKey) {
    throw 'TAURI_SIGNING_PRIVATE_KEY (or TAURI_SIGNING_PRIVATE_KEY_PATH) is not configured. Refusing unsigned release.'
}
if (-not $DryRun) {
    # Build/bundle officially accept inline or path input; do not rewrite the signing environment.
    if ($env:TAURI_SIGNING_PRIVATE_KEY -and $env:TAURI_SIGNING_PRIVATE_KEY_PATH) { throw 'Configure only one private key input.' }
    $privateInput = $env:TAURI_SIGNING_PRIVATE_KEY
    if ($env:TAURI_SIGNING_PRIVATE_KEY_PATH) {
        if (-not (Test-Path -LiteralPath $env:TAURI_SIGNING_PRIVATE_KEY_PATH -PathType Leaf)) { throw 'Private key file is missing or unreadable.' }
        try { $privateInput = [IO.File]::ReadAllText($env:TAURI_SIGNING_PRIVATE_KEY_PATH) } catch { throw 'Private key file is unreadable.' }
    } elseif (Test-Path -LiteralPath $privateInput -PathType Leaf -ErrorAction SilentlyContinue) {
        try { $privateInput = [IO.File]::ReadAllText($privateInput) } catch { throw 'Private key file is unreadable.' }
    }
    $null = Read-SigningEnvelope $privateInput 'private'
    $privateInput = $null
}

$expectedManifestEndpoint = 'https://github.com/newyngwieslash-ops/MovieDesign/releases/latest/download/latest.json'
$expectedPolicyUrl = 'https://raw.githubusercontent.com/newyngwieslash-ops/MovieDesign/main/updates/stable/policy.json'
$expectedReleasesUrl = 'https://github.com/newyngwieslash-ops/MovieDesign/releases/latest'

if (-not $tauriConf.bundle.createUpdaterArtifacts) {
    throw 'tauri.conf.json must enable bundle.createUpdaterArtifacts = true.'
}
if ($tauriConf.plugins.updater.requireSignedVersion -ne $true) { throw 'Updater must requireSignedVersion.' }
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

$targetTriple = (& rustc --print host-tuple).Trim()
if ($LASTEXITCODE -ne 0 -or $targetTriple -cne 'x86_64-pc-windows-msvc') { throw 'Release requires Windows x64.' }
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

if (-not $OutputDir.Trim()) {
    $OutputDir = if ($DryRun) {
        Join-Path $projectRoot '.runtime/release-dry-run'
    } else {
        Join-Path $projectRoot ".runtime/release-v$resolvedVersion"
    }
}
New-Item -ItemType Directory -Force -Path $OutputDir | Out-Null
if (Get-ChildItem -LiteralPath $OutputDir -File | Where-Object { $_.Name -eq 'latest.json' -or $_.Name -like '*-setup.exe*' }) {
    throw 'Output directory contains previous release artifacts; choose a fresh output directory.'
}

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
    return
}

Write-Host "==> Building signed Tauri NSIS installer..."
$buildStarted = [datetime]::UtcNow
Push-Location (Join-Path $projectRoot 'desktop')
try {
    & pnpm tauri build --bundles nsis
    if ($LASTEXITCODE -ne 0) { throw 'Tauri signed release build failed.' }
} finally {
    Pop-Location
}

$nsisDir = Join-Path $projectRoot 'desktop/src-tauri/target/release/bundle/nsis'
$expectedInstaller = "$($tauriConf.productName)_${resolvedVersion}_x64-setup.exe"
$installers = @(Get-ChildItem -LiteralPath $nsisDir -Filter '*-setup.exe' -File)
if ($installers.Count -ne 1) { throw 'Expected exactly one NSIS installer in the build output.' }
$installer = $installers[0]
if ($installer.Name -cne $expectedInstaller) { throw "Expected installer $expectedInstaller." }
if ($installer.Length -eq 0 -or $installer.LastWriteTimeUtc -lt $buildStarted) { throw 'Installer is empty or not fresh from this build.' }
$appBinary = Get-Item -LiteralPath (Join-Path $projectRoot 'desktop/src-tauri/target/release/moviedesign.exe')
if ($appBinary.VersionInfo.ProductVersion -cne $resolvedVersion) { throw 'Built binary version mismatch.' }
$sigFile = "$($installer.FullName).sig"
if (-not (Test-Path -LiteralPath $sigFile)) {
    throw "Signature file missing: $sigFile. Refusing unsigned release."
}
if ((Get-Item -LiteralPath $sigFile).LastWriteTimeUtc -lt $buildStarted) { throw 'Signature is not fresh from this build.' }
$signatureContent = ([IO.File]::ReadAllText($sigFile)).Trim()
try {
    $signatureRaw = [Text.UTF8Encoding]::new($false, $true).GetString([Convert]::FromBase64String($signatureContent))
    $signatureLines = $signatureRaw.Trim() -split '\r?\n'
    if ($signatureLines.Count -ne 4 -or $signatureLines[0] -notmatch '^untrusted comment:' -or $signatureLines[2] -notmatch '^trusted comment: ') { throw 'invalid envelope' }
    $packet = [Convert]::FromBase64String($signatureLines[1])
    if ($packet.Length -ne 74 -or $packet[0] -ne 69 -or $packet[1] -ne 68 -or [Convert]::FromBase64String($signatureLines[3]).Length -ne 64) { throw 'invalid packet' }
} catch { throw 'Invalid installer signature format.' }
if (($signatureLines[2] -split "`t") -cnotcontains "version:$resolvedVersion") { throw 'Installer signature version mismatch.' }
& cargo run --quiet --locked --offline --manifest-path (Join-Path $projectRoot 'desktop/src-tauri/Cargo.toml') --example verify-release -- $installer.FullName $sigFile ([string]$tauriConf.plugins.updater.pubkey) $resolvedVersion
if ($LASTEXITCODE -ne 0) { throw 'Installer signature verification failed. Refusing to generate manifest.' }

$installerDest = Join-Path $OutputDir $installer.Name
$sigDest = Join-Path $OutputDir "$($installer.Name).sig"
Copy-Item -LiteralPath $installer.FullName -Destination $installerDest -Force
Copy-Item -LiteralPath $sigFile -Destination $sigDest -Force

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
