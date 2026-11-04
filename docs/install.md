# Install

**macOS / Linux (Homebrew)**

```sh
brew install aalsanie/tap/distroplane
```

**Windows:** Download the x64 or ARM64 [MSI from the latest release](https://github.com/aalsanie/distroplane/releases/latest). It includes the CLI and providers and requires administrator approval.

**Linux (without Homebrew):**

```sh
curl -fL https://raw.githubusercontent.com/aalsanie/distroplane/v0.9.3/scripts/install-linux.py -o install-linux.py
python3 install-linux.py --version 0.9.3
~/.local/bin/distroplane version
```

The Linux installer requires Python 3.8+. For offline installation use `--offline-dir DIR` with the matching release archive, `RELEASE-METADATA.json`, and `SHA256SUMS`.

Check any installation:

```sh
distroplane version
distroplane help
```

Standalone downloads, checksums, and build attestations are available on the [release page](https://github.com/aalsanie/distroplane/releases/latest). A checksum hosted alongside an archive is an integrity check, not an independent publisher signature.
