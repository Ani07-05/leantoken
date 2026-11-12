# Install slendertoken on Windows:
#   irm https://raw.githubusercontent.com/Ani07-05/slendertoken/main/install.ps1 | iex
# Downloads the single binary from the latest GitHub release, then runs `slendertoken setup`, which installs and
# configures the Bifrost gateway. No Python, Node or Go needed.
# $env:GROQ_API_KEY runs setup without prompting; $env:SLENDERTOKEN_NO_SETUP = "1" skips it.
# $env:SLENDERTOKEN_VERSION = "v0.2.0" pins a release.
$ErrorActionPreference = "Stop"
$repo = "Ani07-05/slendertoken"
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
$file = "slendertoken_windows_$arch.zip"
$base = if ($env:SLENDERTOKEN_VERSION) { "https://github.com/$repo/releases/download/$($env:SLENDERTOKEN_VERSION)" } else { "https://github.com/$repo/releases/latest/download" }

$tmp = Join-Path ([IO.Path]::GetTempPath()) ([Guid]::NewGuid().ToString())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    Write-Host "downloading $file..."
    Invoke-WebRequest "$base/$file" -OutFile "$tmp\$file"
    Invoke-WebRequest "$base/checksums.txt" -OutFile "$tmp\checksums.txt"
    $want = ((Get-Content "$tmp\checksums.txt" | Where-Object { $_ -match " $file$" }) -split "\s+")[0]
    $got = (Get-FileHash "$tmp\$file" -Algorithm SHA256).Hash.ToLower()
    if (-not $want -or $want -ne $got) { throw "checksum mismatch for $file, refusing to install" }

    $dir = Join-Path $env:LOCALAPPDATA "slendertoken\bin"
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    Expand-Archive "$tmp\$file" -DestinationPath $dir -Force

    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if ($userPath -notlike "*$dir*") {
        [Environment]::SetEnvironmentVariable("Path", "$userPath;$dir", "User")
        Write-Host "added $dir to your PATH (open a new terminal)"
    }
    Write-Host ""
    Write-Host "installed $dir\slendertoken.exe"
    if ($env:SLENDERTOKEN_NO_SETUP -eq "1") {
        Write-Host "skipped setup. When ready: slendertoken setup"
    } else {
        # setup downloads Bifrost, stores your Groq key in its config, starts it and tests the whole path
        Write-Host ""
        & "$dir\slendertoken.exe" setup
        if ($LASTEXITCODE -ne 0) { Write-Host "setup did not finish. Fix the problem above, then run: slendertoken setup --force" }
    }
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
