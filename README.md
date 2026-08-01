# Ubuntu Provisioning System

> Fully automated Ubuntu desktop setup — from bare metal to fully configured development environment.

Based on the same toolchain as [provisioner](../provisioner/) (our Windows setup tool), this project automates an Ubuntu install end-to-end using **cloud-init autoinstall** + a **post-install provisioning script**.

---

## 🚀 Quick Start

### 1. Find your target disk serial

On the machine you're going to install Ubuntu on (or from a live USB):

```bash
lsblk -o NAME,SERIAL,MODEL,SIZE
```

Copy the `SERIAL` of the disk you want to install to (e.g. `SAMSUNG_MZVL21T0HCLR_S1234567`).

### 2. Prepare the USB drive

On any Linux machine:

```bash
# Download Ubuntu 24.04 desktop ISO
wget https://releases.ubuntu.com/24.04.1/ubuntu-24.04.1-desktop-amd64.iso

# Run the preparer
sudo ./scripts/prepare-usb.sh \
  --iso ubuntu-24.04.1-desktop-amd64.iso \
  --disk /dev/sdb \
  --serial SAMSUNG_MZVL21T0HCLR_S1234567
```

**WARNING:** This wipes the entire USB disk. Make sure `--disk` points to the right device.

### 3. Install

1. Insert the USB into the target machine
2. Boot from USB (UEFI mode)
3. **Walk away.** The installer runs without any interaction:
   - Partitions the disk (GPT, ext4, no swap)
   - Creates `dailyuser` (password: `1`)
   - Configures automatic login
   - Reboots when done

### 4. First login

After reboot, you'll land on the desktop automatically. The `first-boot.service` triggers `provision.sh`, which installs everything:

- **System:** Docker, GitHub CLI, cc-switch, GNOME Tweaks, snaps
- **Dev tools:** Homebrew → mise → Node/Python/Go/Rust/Java/Bun + pnpm/maven/uv/ruff
- **CLI:** reasonix, opencode, Claude Code
- **Desktop:** Dark theme, dock favorites, keyboard shortcuts
- **Shell:** `.bashrc` with brew + mise activation

Progress is printed to the terminal. When it's done, you'll see a summary.

---

## 📁 What's in the box

```
provisioner-ubuntu/
├── autoinstall/
│   ├── autoinstall.yaml      # cloud-init: disk, user, packages, late commands
│   └── meta-data             # cloud-init requires this (empty)
├── config/
│   ├── apt-packages.list     # System apt packages (1 per line)
│   ├── brew-packages.list    # Homebrew formulas (1 per line)
│   └── mise.toml             # Language runtime versions
# (Snap packages are set in autoinstall.yaml under the `snaps:` field)
├── scripts/
│   ├── prepare-usb.sh        # Build bootable USB from ISO
│   ├── provision.sh          # Post-install setup (runs on first boot)
│   └── first-boot.service    # systemd oneshot: triggers provision.sh
└── dotfiles/                 # (optional) your dotfiles
```

---

## ⚙️ Customization

Edit the config files to change what gets installed:

| File | What it controls |
|---|---|
| `autoinstall.yaml` (`snaps:` field) | Desktop apps via `snap` (firefox, obsidian, code, discord, spotify) |
| `config/apt-packages.list` | System packages via `apt` |
| `config/brew-packages.list` | CLI tools via Homebrew |
| `config/mise.toml` | Language runtimes (Node, Python, Go, etc.) |

Then rebuild the USB with `prepare-usb.sh`.

---

## 🔁 Re-running

`provision.sh` is idempotent — you can run it again anytime:

```bash
sudo provision
```

Already-installed packages are skipped. Useful when you update a config file later and want to apply changes without reinstalling the OS.

---

## 🔐 Security notes

- **dailyuser** has password `1` — change it after setup with `passwd`
- **Auto-login** is enabled via GDM — disable in `Settings → Users` if you prefer a login prompt
- **sudo** still requires the password — only login is automatic
- **No disk encryption** — this is a single-user desktop setup. Enable LUKS in `autoinstall.yaml` if needed

---

## 🐛 Troubleshooting

**The installer didn't auto-start.**
Make sure you're booting in UEFI mode (not Legacy/CSM). Check BIOS settings.

**provision.sh didn't run on first boot.**
Check the journal: `journalctl -u first-boot.service`. Run it manually: `sudo provision`.

**Some packages failed.**
Re-run `sudo provision` — it skips already-installed packages and retries failures.

**cc-switch failed to install.**
The GitHub API may be rate-limited. Wait a few minutes and re-run `sudo provision`.
