# CLAUDE.md

This file provides guidance to Claude Code when working with code in this repository.

## Project Overview

An **Ubuntu desktop provisioning system** that automates fresh machine setup in two phases:

1. **cloud-init autoinstall** — Fully unattended Ubuntu installation (disk partitioning, user creation, base packages, snaps, late-commands) from a USB drive.
2. **provision (first boot)** — Runs once after the install (systemd `first-boot.service`): installs the dev toolchain, desktop config, CLI tools, and Chinese input (fcitx5).

**The implementation is now Go** (since the 2026-08 migration): `cmd/provisioner` is a single ~7 MB static binary replacing the old bash scripts. The bash scripts in `scripts/` remain as reference and for the still-deployed artifacts (`setup-fcitx5-chinese.sh`, `first-boot.service`), but the Go tool is authoritative.

Sibling project: `../provisioner/` (Windows provisioning tool, also Go).

**Target scope:** personal, single-user Ubuntu desktops for day-to-day development — NOT servers or multi-user systems.

## Build & Test

The Go module lives in `go/`:

| Command | What it does |
|---|---|
| `cd go && go test ./...` | Run all unit tests |
| `cd go && go vet ./...` | Vet |
| `cd go && go build -o /tmp/p ./cmd/provisioner` | Build the CLI |
| `/tmp/p config-gen` | Render user-data + grub.cfg from the typed config |
| `/tmp/p test-vm --iso … --payload …` | Validate autoinstall end-to-end in a KVM VM (no root) |
| `sudo /tmp/p provision` | Run first-boot provisioning (root; user phases self re-exec) |
| `sudo /tmp/p usb --iso … --disk /dev/sdX` | Build a bootable autoinstall USB |

The autoinstall user-data is generated with **`gopkg.in/yaml.v3`** — never hand-templated YAML (a template produced invalid YAML for multi-line late-commands, leaving subiquity stuck in state WAITING).

## Architecture

```
go/
  cmd/provisioner/        cobra CLI: config-gen / test-vm / provision / provision-user / usb
  internal/autoinstall/   typed config + user-data/grub.cfg generation (yaml.v3) + table tests
  internal/config/        typed first-boot provisioning config (Default())
  internal/provision/     provision.sh phases ported (root/user split via binary self re-exec)
  internal/usb/           prepare-usb.sh port
  internal/vmtest/        pure-Go verifier (qcow2 via go-qcow2reader + go-diskfs) + ISO repack + qemu boot

scripts/                  bash (reference/superseded):
  setup-fcitx5-chinese.sh — still deployed (fcitx5 user config, called by the Go provision)
  first-boot.service      — still deployed (runs the Go provisioner as /usr/local/bin/provision)
  provision.sh, prepare-usb.sh, test-vm.sh, analyze-install-log.sh — superseded by Go
autoinstall/              the bash-era config + grub template (superseded by Go config)
config/                   apt/brew/mise lists (mirrored into internal/config)
dotfiles/                 copied to /home/dailyuser during provisioning
```

The seed payload (what lands in the ISO's /nocloud/ or the USB's nocloud/) is: the Go `provision` binary, `first-boot.service`, `setup-fcitx5-chinese.sh`, `config/`, `dotfiles/`, plus `user-data`/`meta-data`.

## Hard-won facts (all validated in KVM, 2026-08)

- **grub.cfg MUST escape the `;`** in `ds=nocloud;s=/cdrom/nocloud/` as `\;`. Grub treats an unescaped `;` as a command separator and truncates the kernel cmdline, silently dropping the seed path. (This was a real-machine root cause of interactive fallback.)
- **user-data's first line must be EXACTLY `#cloud-config`** (no space). `# cloud-config` makes cloud-init treat it as unhandled user-data and drop the whole config.
- **The Ubuntu 26.04 desktop installer (ubuntu-desktop-bootstrap) does NOT apply `locale:`/`timezone:` to the target** — the Locale/TimeZone controllers are no-ops (verified via the subiquity debug log). The autoinstall config carries explicit late-commands to set them (locale.conf, locale-gen, /etc/localtime → Asia/Shanghai, tzdata debconf).
- **qemu `-kernel` direct boot does NOT trigger 26.04 desktop autoinstall** — it only engages through the ISO's EFI/grub path. The vmtest harness boots the repacked ISO via `-cdrom`.
- **The 26.04 desktop installer installs in the BACKGROUND of the live session**: subiquity-server Start→Stop→Start then GDM/login is NORMAL, not a fallback. Judge engagement by `target.qcow2` growth + `interactive: false` in the installed system's installer journal — not the console pattern.
- **Pure-Go no-root disk verification**: `github.com/lima-vm/go-qcow2reader` exposes a qcow2 as an `io.ReaderAt`; adapt it to `fs.File` for go-diskfs `OpenBackend` to read GPT + modern ext4 directly. Paths are io/fs-relative (`etc/passwd`, not `/etc/passwd`). go-diskfs does NOT read the qcow2 container itself — you must go through go-qcow2reader.
- **`xorriso -indev ISO -report_el_torito as_mkisofs`** gives the original's boot parameters; reuse them (strip the `'...'` quoting from interval paths — an absolute+quoted path fails to open). Use `-e '--interval:appended_partition_2:all::'` (symbolic), not the resolved form.
- **First-boot provision runs Before=systemd-user-sessions**, so dailyuser's session D-Bus isn't up — the fcitx5 phase degrades gracefully (env/autostart written; pinyin group may need a re-run after login).

## Network gotcha (VM tests)

The host's proxy (clash) path to `archive.ubuntu.com` was measured at ~90 KB/s, which makes the VM installer's `apt-get update` hang and the full install (with network apt/snap) incomplete. This is a HOST proxy-path issue, NOT slirp or the harness. Full provision validation is best done on a real machine.

## Key Design Decisions

- **Disk matching by serial** — autoinstall `match.serial` targets the exact system SSD; no match = installer halts, never touches the wrong disk.
- **Go provisioner self re-exec** — user-owned phases run via `sudo -u dailyuser <binary> provision-user <phase>`; the re-entered process runs with the right HOME/USER and PATH (brew/mise shims).
- **Runner interface** — provision phases take a `Runner` so orchestration + idempotency are unit-testable with a fake.
- **Idempotent everywhere** — each provision phase checks "already done?" before acting (dpkg -s, file existence, brew list, etc.).
- **Error tiers** — core apt packages fail-fast; everything else best-effort with warnings.
- **No LUKS** — single-user desktop scope.
- **Password "1" + GDM auto-login** — sudo still needs the password.
