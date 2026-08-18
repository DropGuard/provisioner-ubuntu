# Ubuntu Provisioning System

**This is for personal, single-user Ubuntu desktops used for day-to-day development. It is not designed for servers or multi-user machines — trying to use it that way will break things, possibly badly.**

> Plug in a USB drive, walk away, come back to a fully configured Ubuntu dev machine.

Zero-interaction desktop setup from bare metal, powered by **cloud-init autoinstall** (orchestrated & tested via Go) + **Ansible modular playbooks** on first boot.

---

## Architecture & Layout

| Directory / File | Responsibility |
|---|---|
| `go/` | Go CLI, USB generator, cloud-init YAML generator & KVM test harness |
| `ansible/roles/` | Modular provisioning roles (`network`, `system`, `packages`, `desktop`, `dev_tools`, `user`) |
| `config/` | Tool & runtime configurations (`mise.toml`, `haruna/`, `proxy-subscription.txt`, `provisioner.yaml`) |
| `dotfiles/` | User dotfiles mirrored to `$HOME` via GNU Stow (`--adopt`) |
| `scripts/` | Bootstrap entrypoints (`bootstrap.sh`, `first-boot.service`) |

---

## Usage

### 1. Customize (optional)

* **Target SSD & USB configuration**: Set target disk parameters in `config/provisioner.yaml`
* **System & desktop packages**: Edit `ansible/roles/packages/tasks/main.yml` and `dev_tools/tasks/main.yml`
* **Language runtimes**: Edit `config/mise.toml` (Node, Python, Go, Rust, etc.)
* **Dotfiles**: Drop your config files into `dotfiles/` (mirrors `$HOME`)

### 2. Build the USB drive (One-step)

From the `go/` directory, run the USB generator (auto-builds payload and writes disk):

```bash
cd go
sudo go run ./cmd/provisioner usb --disk /dev/sdb
```

*(Parameters like `--serial`, `--iso`, or `--config` can also be passed via flags)*

**⚠️ `--disk` is wiped entirely. Double-check you're pointing at the USB drive.**

### 3. Install

1. Insert the USB into the target machine
2. Boot in UEFI mode
3. **Walk away.** The installer runs without interaction: partition → install base OS → seed payload → reboot → run Ansible playbooks → auto-destruct first-boot service → enter KDE Plasma Wayland desktop.

---

## Validating changes (KVM)

No need to rebuild a USB to test Ansible or config changes (run inside `go/` directory):

```bash
# Phase A: full install test (~40 min), produces a golden image
go run ./cmd/provisioner test-vm --iso ubuntu-26.04-live-server-amd64.iso --golden

# Phase B: quick assertions against the golden image (did autoinstall succeed?)
go run ./cmd/provisioner test-e2e --iso ubuntu-26.04-live-server-amd64.iso

# Phase C: run Ansible provisioning against the golden image + assert KDE/SDDM session
go run ./cmd/provisioner test-provision --base /path/to/golden.qcow2 --repo ..
```

---

## Re-running & Granular Maintenance

Provisioning is idempotent. You can re-run the full pipeline or specific tags:

```bash
# Re-run full provisioning
sudo /usr/local/bin/bootstrap-provision.sh

# Or run specific roles via Ansible tags
sudo ansible-playbook -c local /usr/local/share/provisioner-ubuntu/ansible/main.yml --tags dev,dotfiles
```

---

## Security notes

- **dailyuser** password is `1` — change it after setup: `passwd`
- **Auto-login** is enabled via **SDDM** into KDE Plasma Wayland
- **sudo** still requires the password
- **No disk encryption** — single-user desktop setup

---

## Troubleshooting

**Installer didn't auto-start.** Make sure you're booting in UEFI mode, not Legacy/CSM.

**Provisioning failed on first boot.** Check logs with `journalctl -u first-boot.service`, or re-run manually: `sudo /usr/local/bin/bootstrap-provision.sh`.
