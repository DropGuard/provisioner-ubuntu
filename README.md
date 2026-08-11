# Ubuntu Provisioning System

**This is for personal, single-user Ubuntu desktops used for day-to-day development. It is not designed for servers or multi-user machines — trying to use it that way will break things, possibly badly.**

> Plug in a USB drive, walk away, come back to a fully configured Ubuntu dev machine.

Zero-interaction desktop setup from bare metal, powered by **cloud-init autoinstall** + a Go **post-install provisioner**.

---

## Usage

### 1. Clone & build

```bash
git clone <this-repo>
cd provisioner-ubuntu/go
go build -o /tmp/p ./cmd/provisioner
```

### 2. Customize (optional)

Edit the package lists under `config/` and config files under `dotfiles/`. These are read at runtime — no recompilation required:

| File | Controls |
|------|----------|
| `config/apt-packages.list` | System packages via apt |
| `config/brew-packages.list` | CLI tools via Homebrew |
| `config/snap-packages.list` | Snap packages |
| `config/flatpak-packages.list` | Flatpak applications |
| `config/mise.toml` | Language runtimes (Node, Python, Go, etc.) |
| `dotfiles/` | User config files (mirrors `$HOME`) |

### 3. Find your target disk serial

On the target machine (or from a live USB):

```bash
lsblk -o NAME,SERIAL,MODEL,SIZE
```

Copy the `SERIAL` of the disk you want to install to.

### 4. Build the USB drive

```bash
# Assemble the seed payload (binary + scripts + config + dotfiles)
/tmp/p build-payload --out /tmp/payload --repo .. --binary /tmp/p

# Write the USB
sudo /tmp/p usb \
  --iso ubuntu-26.04-live-server-amd64.iso \
  --disk /dev/sdb \
  --serial YOUR_DISK_SERIAL \
  --payload /tmp/payload
```

**⚠️ `--disk` is wiped entirely. Double-check you're pointing at the USB drive.**

### 5. Install

1. Insert the USB into the target machine
2. Boot in UEFI mode
3. **Walk away.** The installer runs without interaction: partition → install → create user → copy payload → reboot

### 6. First boot

After reboot, `first-boot.service` runs the provisioner automatically. Check progress:

```bash
journalctl -u first-boot.service
```

Core packages fail fast — retry with `systemctl restart first-boot.service`. Everything else is best-effort; failures don't block the rest.

---

## Validating changes (KVM)

No need to rebuild a USB to test provisioner or config changes:

```bash
# Phase A: full install test (~40 min), produces a golden image
/tmp/p test-vm --iso ubuntu-26.04-live-server-amd64.iso --payload /tmp/payload --golden

# Phase B: quick assertions against the golden image (did autoinstall succeed?)
/tmp/p test-e2e --iso ubuntu-26.04-live-server-amd64.iso

# Phase C: run the provisioner against the golden image (test config/dotfile changes)
/tmp/p test-provision --base /path/to/golden.qcow2 --binary /tmp/p --repo ..
```

For day-to-day package or dotfile changes, just run Phase C — it takes minutes, not a full reinstall.

---

## Re-running

Provisioning is idempotent — already-installed packages are skipped:

```bash
sudo /usr/local/bin/provision
```

---

## Security notes

- **dailyuser** password is `1` — change it after setup: `passwd`
- **Auto-login** is enabled via GDM — disable in Settings → Users if you prefer a login prompt
- **sudo** still requires the password
- **No disk encryption** — single-user desktop setup

---

## Troubleshooting

**Installer didn't auto-start.** Make sure you're booting in UEFI mode, not Legacy/CSM.

**Provision didn't run on first boot.** Check `journalctl -u first-boot.service`, or run manually: `sudo /usr/local/bin/provision`.

**Some packages failed.** Re-run `sudo /usr/local/bin/provision` — it skips installed packages and retries failures.
