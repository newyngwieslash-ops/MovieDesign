$ErrorActionPreference = 'Stop'
function Assert($Condition, [string]$Message) { if (-not $Condition) { throw $Message } }
$repository = Split-Path $PSScriptRoot -Parent
$fixture = Join-Path ([IO.Path]::GetTempPath()) ("MovieDesign release ' fixture " + [guid]::NewGuid().ToString('N'))
$savedEnv = @{}
foreach ($name in @('GITHUB_REF', 'TAURI_SIGNING_PRIVATE_KEY', 'TAURI_SIGNING_PRIVATE_KEY_PATH', 'TAURI_SIGNING_PRIVATE_KEY_PASSWORD')) {
    $savedEnv[$name] = [Environment]::GetEnvironmentVariable($name)
    [Environment]::SetEnvironmentVariable($name, $null)
}
$script:checks = 0
function Reset-Fixture {
    foreach ($relative in @('scripts', 'desktop/src-tauri', 'updates/stable', 'docs', 'gateway/internal/agent')) {
        New-Item -ItemType Directory -Force -Path (Join-Path $fixture $relative) | Out-Null
    }
    foreach ($relative in @('scripts/release.ps1', 'desktop/src-tauri/tauri.conf.json', 'desktop/src-tauri/Cargo.toml', 'desktop/src-tauri/Cargo.lock', 'desktop/package.json', 'updates/stable/policy.json', 'docs/third-party.md')) {
        Copy-Item -LiteralPath (Join-Path $repository $relative) -Destination (Join-Path $fixture $relative) -Force
    }
    foreach ($name in @('build-media-tools', 'build-gateway', 'build-opencode')) {
        [IO.File]::WriteAllText((Join-Path $fixture "scripts/$name.ps1"), '$global:releaseFixtureBuildCalls++; if ($global:releaseFixtureStopAtBuild) { throw ''fixture build reached'' }')
    }
    $global:releaseFixtureBuildCalls = 0
    $global:releaseFixtureStopAtBuild = $true
    $global:releaseFixtureArtifactMode = 'valid'
    $env:GITHUB_REF = $null
    $env:TAURI_SIGNING_PRIVATE_KEY = $null
    $env:TAURI_SIGNING_PRIVATE_KEY_PATH = $null
}
function Set-FixtureConfig([scriptblock]$Edit) {
    $path = Join-Path $fixture 'desktop/src-tauri/tauri.conf.json'
    $config = [IO.File]::ReadAllText($path) | ConvertFrom-Json
    & $Edit $config
    [IO.File]::WriteAllText($path, ($config | ConvertTo-Json -Depth 12))
}
function Set-StructuralPublicKey {
    # Deliberately non-signing structural vector, only this copied fixture config.
    $packet = [byte[]]::new(42); $packet[0] = 69; $packet[1] = 100; $packet[10] = 1
    $raw = "untrusted comment: fixture public key`n" + [Convert]::ToBase64String($packet)
    $script:fixturePublicKey = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($raw))
    Set-FixtureConfig { param($config) $config.plugins.updater.pubkey = $script:fixturePublicKey }
}
function Expect-Rejected([string]$Name, [string]$Expected, [hashtable]$Arguments = @{}, [int]$BuildCalls = 0) {
    $failure = $null
    try { & (Join-Path $fixture 'scripts/release.ps1') @Arguments } catch { $failure = $_.Exception.Message }
    Assert ($failure -and $failure -match $Expected) "$Name failed: expected '$Expected', got '$failure'"
    Assert ($global:releaseFixtureBuildCalls -eq $BuildCalls) "$Name crossed build guard ($global:releaseFixtureBuildCalls calls)"
    $script:checks++; Write-Host "PASS $Name"
}
function rustc { $global:LASTEXITCODE = 0; 'x86_64-pc-windows-msvc' }
function Get-Item {
    param([string]$LiteralPath)
    if ($LiteralPath -eq (Join-Path $fixture 'desktop/src-tauri/target/release/moviedesign.exe')) {
        $version = if ($global:releaseFixtureArtifactMode -eq 'bad-binary') { '9.0.0' } else { '0.1.0' }
        return [pscustomobject]@{ LastWriteTimeUtc = [datetime]::UtcNow; VersionInfo = [pscustomobject]@{ ProductVersion = $version } }
    }
    Microsoft.PowerShell.Management\Get-Item -LiteralPath $LiteralPath
}
function cargo {
    $global:LASTEXITCODE = if ($global:releaseFixtureArtifactMode -eq 'bad-crypto') { 1 } else { 0 }
}
function pnpm {
    $global:LASTEXITCODE = 0
    if ($args -contains 'tauri') {
        $nsis = Join-Path $fixture 'desktop/src-tauri/target/release/bundle/nsis'
        New-Item -ItemType Directory -Force -Path $nsis | Out-Null
        $installer = Join-Path $nsis 'MovieDesign_0.1.0_x64-setup.exe'
        [IO.File]::WriteAllText($installer, 'fixture installer bytes; never executable')
        $packet = [byte[]]::new(74); $packet[0] = 69; $packet[1] = 68
        $signedVersion = if ($global:releaseFixtureArtifactMode -eq 'bad-version') { '9.0.0' } else { '0.1.0' }
        $signature = "untrusted comment: fixture`n$([Convert]::ToBase64String($packet))`ntrusted comment: timestamp:1`tfile:fixture`tversion:$signedVersion`n$([Convert]::ToBase64String([byte[]]::new(64)))"
        $encoded = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($signature))
        [IO.File]::WriteAllText("$installer.sig", $encoded)
        switch ($global:releaseFixtureArtifactMode) {
            'missing-sig' { Remove-Item -LiteralPath "$installer.sig" }
            'empty-sig' { [IO.File]::WriteAllText("$installer.sig", '') }
            'bad-base64' { [IO.File]::WriteAllText("$installer.sig", 'not base64') }
            'stale' { (Get-Item -LiteralPath $installer).LastWriteTimeUtc = [datetime]'2000-01-01' }
            'stale-sig' { (Get-Item -LiteralPath "$installer.sig").LastWriteTimeUtc = [datetime]'2000-01-01' }
            'empty-installer' { [IO.File]::WriteAllText($installer, '') }
            'multiple' { Copy-Item -LiteralPath $installer -Destination (Join-Path $nsis 'unexpected-setup.exe') }
            'wrong-name' { Move-Item -LiteralPath $installer -Destination (Join-Path $nsis 'MovieDesign_9.0.0_x64-setup.exe') }
        }
    }
}
try {
    Reset-Fixture
    Expect-Rejected 'explicit version mismatch before build' 'version.*mismatch' @{ DryRun = $true; Version = '0.2.0' }
    Reset-Fixture; $env:GITHUB_REF = 'refs/tags/v0.2.0'
    Expect-Rejected 'tag mismatch before build' 'tag.*mismatch' @{ DryRun = $true }
    Reset-Fixture
    $path = Join-Path $fixture 'desktop/package.json'
    [IO.File]::WriteAllText($path, ([IO.File]::ReadAllText($path).Replace('"version": "0.1.0"', '"version": "0.2.0"')))
    Expect-Rejected 'npm version mismatch' 'version.*mismatch' @{ DryRun = $true }
    foreach ($file in @('desktop/src-tauri/Cargo.toml', 'desktop/src-tauri/Cargo.lock')) {
        Reset-Fixture
        $path = Join-Path $fixture $file
        [IO.File]::WriteAllText($path, ([IO.File]::ReadAllText($path).Replace('version = "0.1.0"', 'version = "0.2.0"')))
        Expect-Rejected "$file mismatch" 'version.*mismatch' @{ DryRun = $true }
    }
    foreach ($version in @('vv0.1.0', '0.01.0', '0.1.0-01')) {
        Reset-Fixture
        Expect-Rejected "invalid SemVer $version" 'invalid release version' @{ DryRun = $true; Version = $version }
    }
    Reset-Fixture
    Expect-Rejected 'public test vector refused for production' 'public test' @{}
    Reset-Fixture; Set-StructuralPublicKey
    Expect-Rejected 'missing private key' 'PRIVATE_KEY.*not configured' @{}
    Reset-Fixture; Set-StructuralPublicKey; $env:TAURI_SIGNING_PRIVATE_KEY_PATH = Join-Path $fixture 'missing key'
    Expect-Rejected 'missing private key file' 'private key.*file' @{}
    Reset-Fixture; Set-StructuralPublicKey; $env:TAURI_SIGNING_PRIVATE_KEY = 'invalid fixture, no real key'
    Expect-Rejected 'malformed private key' 'private key.*format' @{}
    Reset-Fixture; Set-FixtureConfig { param($config) $config.plugins.updater.pubkey = 'invalid' }
    Expect-Rejected 'malformed public key' 'public key.*format' @{ DryRun = $true }
    Reset-Fixture; Set-StructuralPublicKey
    Expect-Rejected 'structural public fixture dry-run reaches build' 'fixture build reached' @{ DryRun = $true; Version = 'v0.1.0' } 1
    Reset-Fixture; Set-StructuralPublicKey; $env:GITHUB_REF = 'refs/tags/v0.1.0'
    Expect-Rejected 'matching tag reaches build' 'fixture build reached' @{ DryRun = $true } 1
    Reset-Fixture; Set-StructuralPublicKey; $global:releaseFixtureStopAtBuild = $false
    $binDir = Join-Path $fixture 'desktop/src-tauri/binaries'
    New-Item -ItemType Directory -Path (Join-Path $binDir 'media') -Force | Out-Null
    foreach ($name in @('open-design-gateway-x86_64-pc-windows-msvc.exe', 'moviedesign-opencode-x86_64-pc-windows-msvc.exe', 'media/ffprobe.exe', 'media/LICENSE')) {
        [IO.File]::WriteAllText((Join-Path $binDir $name), 'fixture')
    }
    $source = Join-Path $fixture 'gateway/internal/agent/media_prompt.go'
    [IO.File]::WriteAllBytes($source, [byte[]]@(239,187,191,39,255,13,10))
    $hash = (Get-FileHash -LiteralPath $source).Hash
    & (Join-Path $fixture 'scripts/release.ps1') -DryRun -OutputDir (Join-Path $fixture 'dry run output')
    Assert ((Get-FileHash -LiteralPath $source).Hash -eq $hash) 'release changed source bytes'
    Assert (-not (Test-Path -LiteralPath (Join-Path $fixture 'dry run output/latest.json'))) 'dry-run emitted manifest'
    $script:checks++; Write-Host 'PASS dry-run preserves source and emits no signed manifest'
    # Structurally valid non-signing private fixture; native build and cryptographic verifier are injected above.
    $packet = [byte[]]::new(158); $packet[0] = 69; $packet[1] = 100
    $raw = "untrusted comment: fixture secret key`n$([Convert]::ToBase64String($packet))"
    $keyPath = Join-Path $fixture "fixture ' key"
    [IO.File]::WriteAllText($keyPath, [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($raw)))
    $env:TAURI_SIGNING_PRIVATE_KEY_PATH = $keyPath
    foreach ($case in @(@('missing-sig','Signature file missing'), @('empty-sig','signature.*format'), @('bad-base64','signature.*format'), @('bad-version','signature.*version'), @('stale','fresh'), @('stale-sig','fresh'), @('empty-installer','empty'), @('bad-binary','binary version mismatch'), @('multiple','exactly one'), @('wrong-name','expected installer'), @('bad-crypto','signature verification failed'))) {
        $nsis = Join-Path $fixture 'desktop/src-tauri/target/release/bundle/nsis'
        if (Test-Path -LiteralPath $nsis) { Get-ChildItem -LiteralPath $nsis -File | Remove-Item -Force }
        $global:releaseFixtureArtifactMode = $case[0]; $global:releaseFixtureBuildCalls = 0
        Expect-Rejected $case[0] $case[1] @{ OutputDir = (Join-Path $fixture ('output-' + $case[0])) } 3
        Assert (-not (Test-Path -LiteralPath (Join-Path $fixture ('output-' + $case[0] + '/latest.json')))) 'rejected artifact emitted manifest'
    }
    Get-ChildItem -LiteralPath $nsis -File | Remove-Item -Force
    $global:releaseFixtureArtifactMode = 'valid'
    & (Join-Path $fixture 'scripts/release.ps1') -OutputDir (Join-Path $fixture 'verified-output')
    $manifest = [IO.File]::ReadAllText((Join-Path $fixture 'verified-output/latest.json')) | ConvertFrom-Json
    Assert ($manifest.version -eq '0.1.0' -and $manifest.platforms.'windows-x86_64'.url.EndsWith('/v0.1.0/MovieDesign_0.1.0_x64-setup.exe')) 'manifest lost version binding'
    Assert ($manifest.platforms.'windows-x86_64'.signature -ceq [IO.File]::ReadAllText((Join-Path $nsis 'MovieDesign_0.1.0_x64-setup.exe.sig'))) 'manifest changed official signature encoding'
    $script:checks++; Write-Host 'PASS manifest uses verified current artifact (native verifier explicitly injected)'
    Assert ($env:TAURI_SIGNING_PRIVATE_KEY_PATH -eq $keyPath -and -not $env:TAURI_SIGNING_PRIVATE_KEY) 'release changed signing environment'
    $env:TAURI_SIGNING_PRIVATE_KEY = [IO.File]::ReadAllText($keyPath); $env:TAURI_SIGNING_PRIVATE_KEY_PATH = $null
    $global:releaseFixtureStopAtBuild = $true; $global:releaseFixtureBuildCalls = 0
    Expect-Rejected 'official inline private input reaches build without env rewrite' 'fixture build reached' @{} 1
    Assert ($env:TAURI_SIGNING_PRIVATE_KEY -ceq [IO.File]::ReadAllText($keyPath)) 'inline signing environment changed'
    $env:TAURI_SIGNING_PRIVATE_KEY = $keyPath; $global:releaseFixtureBuildCalls = 0
    Expect-Rejected 'official private string-or-path input reaches build' 'fixture build reached' @{} 1
    Assert ($env:TAURI_SIGNING_PRIVATE_KEY -ceq $keyPath) 'path signing environment changed'
    # Exercise the real generator against LF/CRLF source copies, with native Go build injected.
    Copy-Item -LiteralPath (Join-Path $repository 'scripts/build-gateway.ps1') -Destination (Join-Path $fixture 'scripts/build-gateway.ps1') -Force
    New-Item -ItemType Directory -Force -Path (Join-Path $fixture 'agent/skills'), (Join-Path $fixture 'gateway') | Out-Null
    function go { $global:LASTEXITCODE = 0 }
    $previousCgo = $env:CGO_ENABLED
    $generatedHashes = @()
    foreach ($newline in @("`n", "`r`n")) {
        [IO.File]::WriteAllText((Join-Path $fixture 'agent/media-agent.md'), "prompt${newline}semantic edit${newline}")
        [IO.File]::WriteAllText((Join-Path $fixture 'agent/skills/fixture.md'), "skill${newline}semantic edit${newline}")
        $sourceHash = (Get-FileHash -LiteralPath (Join-Path $fixture 'agent/media-agent.md')).Hash
        & (Join-Path $fixture 'scripts/build-gateway.ps1')
        Assert ((Get-FileHash -LiteralPath (Join-Path $fixture 'agent/media-agent.md')).Hash -eq $sourceHash) 'generator rewrote markdown source bytes'
        $generatedHashes += ((Get-FileHash -LiteralPath (Join-Path $fixture 'gateway/internal/agent/media_prompt.go')).Hash + (Get-FileHash -LiteralPath (Join-Path $fixture 'gateway/internal/agent/skills_bundle.go')).Hash)
    }
    Assert ($generatedHashes[0] -eq $generatedHashes[1]) 'LF/CRLF generated different source'
    Assert ($env:CGO_ENABLED -eq $previousCgo) 'generator changed CGO environment'
    Assert ([IO.File]::ReadAllText((Join-Path $fixture 'gateway/internal/agent/media_prompt.go')).Contains('semantic edit')) 'generator lost semantic edits'
    $script:checks++; Write-Host 'PASS generator deterministic LF/CRLF, semantic edits retained, environment restored'
    Write-Host "Release checks passed ($script:checks cases). No real build, signing, installer, publishing or network."
} finally {
    foreach ($name in $savedEnv.Keys) { [Environment]::SetEnvironmentVariable($name, $savedEnv[$name]) }
    $tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
    if (-not $fixture.StartsWith($tempRoot, [StringComparison]::OrdinalIgnoreCase) -or (Split-Path $fixture -Leaf) -notlike "MovieDesign release ' fixture *") { throw 'Fixture cleanup escaped temporary directory.' }
    if (Test-Path -LiteralPath $fixture) { Remove-Item -LiteralPath $fixture -Recurse -Force }
}
