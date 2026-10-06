# Rebuilds vb and installs it onto PATH.
#
# Smart App Control blocks unsigned binaries by file hash, and its verdict for a
# given hash can flip from allow to block hours or days after the build. Each
# rebuild produces a new hash and buys another reprieve, so this script exists to
# make that one command. The durable fixes are to run vb under WSL or to turn
# Smart App Control off; see docs/windows.md.

$ErrorActionPreference = 'Stop'

$repo = Split-Path -Parent $PSScriptRoot
$go = Join-Path $HOME 'sdk\go1.27.1\bin\go.exe'
if (-not (Test-Path $go)) { $go = 'go' }

$out = Join-Path $repo 'bin\vb.exe'
$dest = Join-Path $HOME 'AppData\Local\agy\bin\vb.exe'

Write-Host "building $out"
& $go build -ldflags='-s -w' -o $out (Join-Path $repo 'cmd\vb')
if ($LASTEXITCODE -ne 0) { throw "go build failed with exit code $LASTEXITCODE" }

Write-Host "installing to $dest"
Copy-Item $out $dest -Force

& $dest --version
if ($LASTEXITCODE -ne 0) {
    throw "vb was installed but will not run; Smart App Control likely blocked it. See docs/windows.md."
}
Write-Host 'ok'
