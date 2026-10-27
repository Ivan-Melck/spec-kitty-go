# Install the spec-kitty executable from a GitHub release (Windows).
#   irm https://raw.githubusercontent.com/<repo>/<ref>/scripts/install.ps1 | iex
# Environment:
#   SPEC_KITTY_REPO         GitHub owner/name hosting releases (default below)
#   SPEC_KITTY_VERSION      release version without "v" (default: newest release,
#                           including release candidates)
#   SPEC_KITTY_INSTALL_DIR  destination directory
#                           (default: %LOCALAPPDATA%\Programs\spec-kitty)
#   SPEC_KITTY_DOWNLOAD_URL base URL holding the archives and SHA256SUMS, for
#                           mirrors (requires SPEC_KITTY_VERSION)
$ErrorActionPreference = 'Stop'
$Repo = if ($env:SPEC_KITTY_REPO) { $env:SPEC_KITTY_REPO } else { 'Ivan-Melck/spec-kitty-go' }
$Dest = if ($env:SPEC_KITTY_INSTALL_DIR) { $env:SPEC_KITTY_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\spec-kitty' }
$Version = $env:SPEC_KITTY_VERSION

$Arch = switch ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture) {
  'X64' { 'amd64' }
  'Arm64' { 'arm64' }
  default { throw "spec-kitty install: unsupported CPU architecture $_" }
}
if (-not $Version) {
  if ($env:SPEC_KITTY_DOWNLOAD_URL) { throw 'spec-kitty install: SPEC_KITTY_DOWNLOAD_URL requires SPEC_KITTY_VERSION' }
  $releases = Invoke-RestMethod "https://api.github.com/repos/$Repo/releases?per_page=1"
  if (-not $releases) { throw "spec-kitty install: no release found in $Repo" }
  $Version = $releases[0].tag_name -replace '^v', ''
}

$Name = "spec-kitty_${Version}_windows_$Arch"
$Base = if ($env:SPEC_KITTY_DOWNLOAD_URL) { $env:SPEC_KITTY_DOWNLOAD_URL } else { "https://github.com/$Repo/releases/download/v$Version" }
$Tmp = Join-Path ([System.IO.Path]::GetTempPath()) ([System.IO.Path]::GetRandomFileName())
New-Item -ItemType Directory -Path $Tmp | Out-Null
try {
  $Zip = Join-Path $Tmp "$Name.zip"
  Invoke-WebRequest -UseBasicParsing "$Base/$Name.zip" -OutFile $Zip
  $Sums = (Invoke-WebRequest -UseBasicParsing "$Base/SHA256SUMS").Content
  if ($Sums -is [byte[]]) { $Sums = [System.Text.Encoding]::UTF8.GetString($Sums) }
  $Line = $Sums -split "`n" | Where-Object { ($_ -split '\s+')[1] -eq "$Name.zip" } | Select-Object -First 1
  if (-not $Line) { throw "spec-kitty install: SHA256SUMS has no entry for $Name.zip" }
  $Expected = ($Line -split '\s+')[0].ToLowerInvariant()
  $Actual = (Get-FileHash -Algorithm SHA256 $Zip).Hash.ToLowerInvariant()
  if ($Expected -ne $Actual) { throw "spec-kitty install: checksum mismatch for $Name.zip" }
  Expand-Archive -Path $Zip -DestinationPath $Tmp
  New-Item -ItemType Directory -Force -Path $Dest | Out-Null
  Copy-Item -Force (Join-Path $Tmp "$Name\spec-kitty.exe") (Join-Path $Dest 'spec-kitty.exe')
} finally {
  Remove-Item -Recurse -Force $Tmp
}
Write-Host "Installed spec-kitty $Version to $(Join-Path $Dest 'spec-kitty.exe')"
$UserPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (-not (($UserPath -split ';') -contains $Dest)) {
  Write-Host "Add $Dest to your user PATH to run spec-kitty from any directory."
}
