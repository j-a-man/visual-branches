# Running vb on Windows

## Smart App Control blocks unsigned builds

If `vb` fails with:

```
Program 'vb.exe' failed to run: An Application Control policy has blocked this file
```

the cause is Smart App Control, not anything about your install.
Confirm it with:

```powershell
Get-ItemProperty 'HKLM:\SYSTEM\CurrentControlSet\Control\CI\Policy' | Select-Object VerifiedAndReputablePolicyState
```

`1` means Smart App Control is enforcing.
The block itself is logged under **Microsoft-Windows-CodeIntegrity/Operational**, event ID 3077, naming policy `{0283ac0f-fff1-49ae-ada1-8a933130cad6}`.

Smart App Control decides per file hash.
An unsigned binary it has never seen is usually allowed at first, and the cloud verdict can flip to *block* hours or days later - the same unchanged binary that ran fine yesterday starts failing today.
Two consequences worth internalizing:

- Rebuilding is not a fix.
  A new build has a new hash and gets another reprieve, then is blocked in turn.
- A self-signed certificate does not help.
  Smart App Control does not honor your local trust store.

## What actually works

### Run vb under WSL (recommended for local development)

WSL runs ELF binaries, which Smart App Control does not police.
This is permanent and needs no security changes.

```bash
GOOS=linux GOARCH=amd64 go build -o bin/vb-linux ./cmd/vb
```

Then, inside your distro:

```bash
mkdir -p ~/.local/bin
cp /mnt/c/path/to/visual-branches/bin/vb-linux ~/.local/bin/vb
chmod +x ~/.local/bin/vb
```

Ubuntu's `~/.profile` puts `~/.local/bin` on PATH only if the directory already exists at login, so open a new shell afterward.
vb reads repositories under `/mnt/c` fine.

### Turn Smart App Control off

Windows Security → App & browser control → Smart App Control settings → Off.

**This is one-way.** Once off, re-enabling requires resetting or reinstalling Windows. Decide accordingly.

### Install a signed release

Released binaries are signed, so they are not subject to any of the above.
Only locally built development binaries hit this.

## Rebuilding as a stopgap

Until one of the above is in place, `scripts/install-local.ps1` rebuilds and reinstalls in one command:

```powershell
.\scripts\install-local.ps1
```

Expect to re-run it whenever the block returns.
