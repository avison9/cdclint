# Installing cdclint

cdclint is one static binary with no runtime dependencies. Every way below
ends with `cdclint` on your PATH; check with:

```
cdclint --version
```

Release archives are named `cdclint_<version>_<os>_<arch>` and published on
the [Releases page](https://github.com/avison9/cdclint/releases). `<os>` is
`linux`, `darwin` (macOS) or `windows`; `<arch>` is `amd64` (Intel and AMD)
or `arm64` (Apple Silicon, Graviton, Raspberry Pi 4 and later, Windows on
ARM).

## macOS

**Homebrew** (recommended):

```
brew install avison9/tap/cdclint
```

That builds from the tagged source, the way homebrew/core does: Homebrew
installs `go` as a build dependency (once, from a bottle), then the build
takes seconds. For the release binary instead, no Go involved:

```
brew install --cask avison9/tap/cdclint
```

Updates come with `brew upgrade` either way. The cask clears the
quarantine attribute after install, so Gatekeeper does not block the
first run; a formula build is never quarantined.

**Release archive**, without Homebrew:

```
VERSION=0.4.1
ARCH=$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')
curl -sSLO "https://github.com/avison9/cdclint/releases/download/v$VERSION/cdclint_${VERSION}_darwin_${ARCH}.tar.gz"
tar -xzf "cdclint_${VERSION}_darwin_${ARCH}.tar.gz" cdclint
sudo install -m 0755 cdclint /usr/local/bin/cdclint
```

macOS quarantines binaries downloaded by a browser (not by `curl`). If
you downloaded through a browser and the first run says the binary
"cannot be opened", clear the attribute:

```
xattr -d com.apple.quarantine /usr/local/bin/cdclint
```

## Linux

**Homebrew on Linux** works the same as on macOS, formula or cask:

```
brew install avison9/tap/cdclint
brew install --cask avison9/tap/cdclint
```

**Release archive** (any distribution, no package manager needed):

```
VERSION=0.4.1
ARCH=$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')
curl -sSLO "https://github.com/avison9/cdclint/releases/download/v$VERSION/cdclint_${VERSION}_linux_${ARCH}.tar.gz"
tar -xzf "cdclint_${VERSION}_linux_${ARCH}.tar.gz" cdclint
sudo install -m 0755 cdclint /usr/local/bin/cdclint
```

Without root, install into `~/.local/bin` (on PATH by default on most
distributions) instead of `/usr/local/bin`.

`.deb` and `.rpm` packages are not published yet. Ask in an issue if your
fleet needs them; goreleaser can produce both.

## Windows

**Release archive** (PowerShell):

```powershell
$Version = "0.4.1"
$Arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
$Dir = "$env:LOCALAPPDATA\Programs\cdclint"
New-Item -ItemType Directory -Force $Dir | Out-Null
Invoke-WebRequest "https://github.com/avison9/cdclint/releases/download/v$Version/cdclint_${Version}_windows_${Arch}.zip" -OutFile "$Dir\cdclint.zip"
Expand-Archive "$Dir\cdclint.zip" -DestinationPath $Dir -Force
Remove-Item "$Dir\cdclint.zip"
```

Then add the directory to your user PATH once:

```powershell
[Environment]::SetEnvironmentVariable("Path", "$env:Path;$Dir", "User")
```

Open a new terminal and run `cdclint --version`. Windows Subsystem for
Linux users should follow the Linux section inside WSL instead.

`winget` and `scoop` manifests are not published yet.

## With Go

On any OS with Go 1.25 or later:

```
go install github.com/avison9/cdclint/cmd/cdclint@latest
```

The binary lands in `$(go env GOPATH)/bin`, usually `~/go/bin`; make sure
that is on your PATH. Before the first tagged release, use `@main` instead
of `@latest`. Note that a `go install` build reports `cdclint dev` from
`--version` rather than a release number.

## In GitHub Actions

No install step. The action downloads the release for the runner and
verifies its checksum:

```yaml
- uses: avison9/cdclint@v0
  with:
    migrations: db/migrations
    connector: cdc/postgres-source.json
    sink: analytics/schema
```

Pin a version with `version: v0.4.1` if you want to control upgrades.

## Docker

No image is published yet. Until there is one, the Go image builds it in a
few seconds:

```
docker run --rm -v "$PWD":/repo -w /repo golang:1.25 \
  sh -c "go install github.com/avison9/cdclint/cmd/cdclint@latest && cdclint --migrations db/migrations --connector cdc/postgres-source.json --sink analytics/schema"
```

## Verifying a download

Every release ships `checksums.txt`, and that file is signed with
[cosign](https://github.com/sigstore/cosign), keyless, by the release
workflow of this repository. Two levels of check:

**1. The archive matches the checksum** (proves the download is intact):

```
curl -sSLO "https://github.com/avison9/cdclint/releases/download/v$VERSION/checksums.txt"
sha256sum -c checksums.txt --ignore-missing        # Linux
shasum -a 256 -c checksums.txt --ignore-missing    # macOS
```

PowerShell:

```powershell
(Get-FileHash "cdclint_${Version}_windows_${Arch}.zip").Hash.ToLower()
Select-String "windows_${Arch}.zip" checksums.txt
```

**2. The checksums were produced by this repository's workflow** (proves
nobody swapped the release):

```
curl -sSLO "https://github.com/avison9/cdclint/releases/download/v$VERSION/checksums.txt.pem"
curl -sSLO "https://github.com/avison9/cdclint/releases/download/v$VERSION/checksums.txt.sig"
cosign verify-blob \
  --certificate checksums.txt.pem \
  --signature checksums.txt.sig \
  --certificate-identity-regexp 'github.com/avison9/cdclint' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
```

The GitHub Action does the first check on every run.

## Uninstalling

| installed with | remove with |
|---|---|
| Homebrew | `brew uninstall cdclint`, or `brew uninstall --cask cdclint` for the cask |
| release archive | delete the binary from where you put it |
| Go | `rm "$(go env GOPATH)/bin/cdclint"` |
| Windows archive | delete `%LOCALAPPDATA%\Programs\cdclint` and the PATH entry |

cdclint writes nothing outside its working directory: no config files, no
cache, no telemetry.
