# Install the cohive CLI

[中文](install-cli.md)

`cohive` on a member's computer pairs the computer (`cohive login`), initializes projects (`cohive init`), and acts as the local forwarder for coding tools (`cohive mcp-proxy`). It is downloaded from **your team's own Cohive server**: you don't need Go, you don't need access to the code repository, and the version always matches the server.

In the web UI, Profile → Pair a new computer shows the commands with your server address filled in, ready to copy. Below, `https://cohive.example.com` stands for your server address.

## Install

**macOS, Linux** (run in a terminal):

```bash
curl -fsSL https://cohive.example.com/install.sh | sh
```

**Windows** (run in PowerShell):

```powershell
irm https://cohive.example.com/install.ps1 | iex
```

When it finishes, run the next step as prompted:

```bash
cohive login --server https://cohive.example.com
```

**Install and pair with one command** (this is the one shown under Profile): after installing, it continues straight into `cohive login`, and you only need to click Authorize in the browser that opens.

```bash
curl -fsSL https://cohive.example.com/install.sh | sh -s -- --login
```

```powershell
$env:COHIVE_LOGIN=1; irm https://cohive.example.com/install.ps1 | iex
```

Without an interactive terminal (for example inside a script), it only installs and tells you to run `cohive login` yourself.

To upgrade, run the install command again; it replaces the binary in place. `cohive version` shows the local version. When `cohive login` or `cohive status` finds that the local version differs from the server's, it warns you and prints the reinstall command (a warning only, nothing stops working).

## Where it is installed

| System | Location | PATH |
|---|---|---|
| macOS, Linux | `~/.local/bin/cohive`; set `COHIVE_INSTALL_DIR` to change it | Shell config files are not modified. If the directory is not on your PATH, the script prints the line to add to `~/.zshrc` or `~/.bashrc` |
| Windows | `%LOCALAPPDATA%\Programs\cohive\cohive.exe`; set `COHIVE_INSTALL_DIR` to change it | Added to the current user's PATH automatically (no admin rights needed); reopen any terminal that is already open |

**Your coding tool must be able to find `cohive` on its PATH**: the command that `cohive init` writes into the project config is just `cohive` (the config is committed to the repository, so it cannot contain one person's absolute path). When a coding tool is not started from a terminal (for example a desktop app launched from the Dock), its PATH may not include `~/.local/bin`. If you see "cohive not found", start the coding tool from a terminal, or add the directory to your login shell's PATH. `cohive init` also warns you when it does not find `cohive` on the current PATH.

## Supported platforms

macOS (Apple silicon, Intel), Linux (x86-64, arm64) and Windows (x64). The installers are generated when the server is built (`make cli-dist`, part of `make build`). A server built without installers shows "contact your admin" in the web UI and in the install scripts.

## Security notes

- **Downloads come only from this server**: the download address in the script is filled in by the server from the address you used for this request, so it never points elsewhere. Before installing, the script checks the SHA-256; on a mismatch it deletes the downloaded file and does not install.
- **What the checksum does and doesn't protect against**: the checksum and the binary come from the same server, so it detects corruption in transit, truncated downloads and the wrong platform; it does **not** protect against a compromised server. Real origin authentication needs signing, which will come when the repository is published.
- **Use HTTPS**: over HTTPS, TLS protects the transfer. Use `http://` addresses only on an internal network; the script warns you.
- **If you don't want to run `curl | sh`**: download it, read it, then run it:

  ```bash
  curl -fsSLo install.sh https://cohive.example.com/install.sh
  less install.sh
  sh install.sh
  ```

  You can also download manually from `https://cohive.example.com/dl/cohive/<platform>` (the platform is `darwin-arm64`, `darwin-amd64`, `linux-amd64`, `linux-arm64` or `windows-amd64`) and verify it against `https://cohive.example.com/dl/cohive/SHA256SUMS`.
- **macOS won't show an "unidentified developer" warning**: files downloaded with `curl` carry no quarantine attribute, so unsigned programs run directly. Files downloaded through a browser are blocked by the system, so we recommend the install script only.
- **Downloading needs no sign-in**: the CLI contains no secrets and can't do anything until it is paired, and new teammates need to install it before they have an account.
- **No admin rights needed**, and nothing keeps running in the background.

## Uninstall

Delete `cohive` from where it was installed (on Windows, also remove that directory from the user PATH), and delete the credentials directory `~/.config/cohive/`. We recommend revoking this computer first under Profile → My devices in the web UI.
