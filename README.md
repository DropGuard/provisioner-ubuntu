# Ubuntu Provisioning System

> Fully automated Ubuntu desktop setup — from bare metal to fully configured development environment.

This project automates an Ubuntu install end-to-end using **cloud-init autoinstall** + a **post-install provisioner**, implemented as a single Go binary (`go/`). The bash-era scripts in `scripts/` remain only as deployed artifacts (`first-boot.service`, `setup-fcitx5-chinese.sh`) — the Go tool is authoritative.

---

## Quick Start

### 1. Build the tool

```bash
cd go
go build -o /tmp/p ./cmd/provisioner
```

### 2. Find your target disk serial

On the machine you're going to install Ubuntu on (or from a live USB):

```bash
lsblk -o NAME,SERIAL,MODEL,SIZE
```

Copy the `SERIAL` of the disk you want to install to (e.g. `SAMSUNG_MZVL21T0HCLR_S1234567`).

### 3. Assemble the seed payload

`build-payload` packs the provisioner binary, the still-deployed scripts, and `config/` + `dotfiles/` into the tree the installer plants under `nocloud/`:

```bash
/tmp/p build-payload --out /tmp/payload --repo . --binary /tmp/p
```

### 4. Build the USB drive

```bash
# Download Ubuntu 26.04 desktop ISO
wget https://releases.ubuntu.com/26.04/ubuntu-26.04-desktop-amd64.iso

sudo /tmp/p usb \
  --iso ubuntu-26.04-desktop-amd64.iso \
  --disk /dev/sdb \
  --serial SAMSUNG_MZVL21T0HCLR_S1234567 \
  --payload /tmp/payload
```

**WARNING:** This wipes the entire USB disk. Make sure `--disk` points to the right device.

### 5. Install

1. Insert the USB into the target machine
2. Boot from USB (UEFI mode)
3. **Walk away.** The installer runs without any interaction:
   - Partitions the disk (GPT, ext4, no swap) — **matched by serial**, so it never touches another disk
   - Creates `dailyuser` (password: `1`)
   - Copies the payload and enables `first-boot.service`
   - Configures automatic login, then reboots

### 6. First login

After reboot, `first-boot.service` runs `/usr/local/bin/provision`, which installs everything:

- **System:** Docker, GitHub CLI, cc-switch, GNOME Tweaks, snaps
- **Dev tools:** Homebrew → mise → Node/Python/Go/Rust/Java/Bun + pnpm/maven/uv/ruff
- **CLI:** reasonix, opencode, Claude Code
- **Desktop:** Dark theme, dock favorites, keyboard shortcuts, Chinese input (fcitx5)
- **Shell:** `.bashrc` with brew + mise activation

Progress is printed to the journal (`journalctl -u first-boot.service`). **Core packages fail fast** — if they fail, the service is marked failed and you can retry with `systemctl restart first-boot.service` after fixing the cause. Everything else is best-effort with warnings.

---

## Validating in a VM (KVM machine)

On a machine with CPU virtualization:

```bash
# Full install in a VM (~40 min): builds the repacked ISO, boots it, waits
/tmp/p test-vm --iso ubuntu-26.04-desktop-amd64.iso --payload /tmp/payload --work /tmp/vmtest

# Assert the installed disk actually contains a successful autoinstall
/tmp/p verify-disk --disk /tmp/vmtest/target.qcow2
```

`verify-disk` checks the partition layout (ESP + ext4 root) and the files the installer and late-commands must have produced.

---

## What's in the box

```
provisioner-ubuntu/
├── go/                      # Go module (authoritative)
│   ├── cmd/provisioner/     # cobra CLI: config-gen, build-payload, test-vm, verify-disk, provision, usb
│   └── internal/
│       ├── autoinstall/     # typed config → user-data + grub.cfg (yaml.v3)
│       ├── config/          # typed provisioning config + runtime Load() from config/
│       ├── payload/         # build-payload assembly
│       ├── provision/       # provisioning phases (root/user split via self re-exec)
│       ├── paths/           # on-target deployment paths (single source)
│       ├── usb/             # USB build
│       └── vmtest/          # pure-Go disk verifier + QEMU harness
├── config/                  # apt/brew/mise lists — single source of truth, read at runtime
├── dotfiles/                # copied to /home/dailyuser during provisioning
└── scripts/                 # deployed artifacts only (first-boot.service, setup-fcitx5, fav, test-env-loading)
```

---

## Customization

| File | What it controls |
|---|---|
| `config/apt-packages.list` | System packages via `apt` (`# --- core` section = fail-fast, rest = best-effort) |
| `config/brew-packages.list` | CLI tools via Homebrew |
| `config/mise.toml` | Language runtimes (Node, Python, Go, etc.) |

Edit the files, then rebuild the payload and the USB. The provisioner reads them at runtime — **no recompile needed**.

To set git identity during provisioning, export these before the first boot (e.g. in `scripts/first-boot.service`):

```bash
GIT_USER_NAME="Your Name"
GIT_USER_EMAIL="you@example.com"
```

---

## Re-running

Provisioning is idempotent — you can run it again anytime:

```bash
sudo /usr/local/bin/provision
```

Already-installed packages are skipped (checked via `dpkg-query`). Useful when you update a config file later and want to apply changes without reinstalling the OS.

---

## Security notes

- **dailyuser** has password `1` — change it after setup with `passwd`
- **Auto-login** is enabled via GDM — disable in `Settings → Users` if you prefer a login prompt
- **sudo** still requires the password — only login is automatic
- **No disk encryption** — this is a single-user desktop setup

---

## Troubleshooting

**The installer didn't auto-start.**
Make sure you're booting in UEFI mode (not Legacy/CSM). Check BIOS settings.

**provision didn't run on first boot.**
Check the journal: `journalctl -u first-boot.service`. Run it manually: `sudo /usr/local/bin/provision`.

**Some packages failed.**
Re-run `sudo /usr/local/bin/provision` — it skips already-installed packages and retries failures.

**cc-switch failed to install.**
The GitHub API may be rate-limited. Wait a few minutes and re-run `provision`.
