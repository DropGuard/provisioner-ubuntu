# CLAUDE.md

This file provides guidance to Claude Code when working with code in this repository.

## Project Overview

An **Ubuntu desktop provisioning system** that automates fresh machine setup in two phases:

1. **cloud-init autoinstall** — Fully unattended Ubuntu installation (disk partitioning, user creation, base packages, late-commands) from a USB drive.
2. **provision (first boot)** — Runs once after install (systemd `first-boot.service` invoking `bootstrap.sh`): executes modular Ansible roles (`network`, `system`, `packages`, `desktop`, `dev_tools`, `user`), and automatically disables `first-boot.service` on finish.

**Architecture split**:
- **Go CLI (`cmd/provisioner`)**: Handles configuration generation (`config-gen`), bootable USB creation (`usb`), seed payload assembly (`build-payload`), and KVM snapshot test orchestration (`test-vm`, `test-e2e`, `test-provision`).
- **Ansible Playbooks (`ansible/`)**: Authoritative engine for first-boot provisioning, package installation, Wayland/NVIDIA configuration, font setup, and dotfile linking.

Sibling project: `../provisioner/` (Windows provisioning tool, also Go).

**Target scope:** personal, single-user Ubuntu desktops for day-to-day development — NOT servers or multi-user systems.

## Build & Test

The Go module lives in `go/`:

| Command | What it does |
|---|---|
| `cd go && go test ./...` | Run all unit tests |
| `cd go && go test ./internal/autoinstall -run TestUserDataGolden -update` | Regenerate the golden user-data snapshot after an intentional change |
| `cd go && go vet ./...` | Vet |
| `cd go && go build -o /tmp/p ./cmd/provisioner` | Build the CLI |
| `/tmp/p config-gen` | Render user-data + grub.cfg from the typed config |
| `/tmp/p build-payload --out /tmp/payload --repo ..` | Assemble the nocloud seed payload (scripts + config + dotfiles + ansible) |
| `/tmp/p test-vm --iso … --payload …` | Validate autoinstall end-to-end in a KVM VM (no root) |
| `/tmp/p test-e2e --iso …` | Quick golden-image assertions via SSH |
| `/tmp/p test-provision --base golden.qcow2 --repo ..` | Run Ansible provisioning on golden snapshot and assert desktop session |
| `sudo /tmp/p usb --iso … --disk /dev/sdX` | Build a bootable autoinstall USB |

The autoinstall user-data is generated with **`gopkg.in/yaml.v3`** — never hand-templated YAML (a template produced invalid YAML for multi-line late-commands, leaving subiquity stuck in state WAITING).

## Architecture

```
go/
  cmd/provisioner/        cobra CLI: config-gen / test-vm / test-e2e / test-provision / usb / build-payload
  internal/autoinstall/   typed config + user-data/grub.cfg generation (yaml.v3) + table tests
  internal/paths/         canonical paths on target system
  internal/payload/       nocloud seed payload builder
  internal/usb/           partitioning, FAT/ext4 filesystem formatting, and ISO extraction
  internal/vmtest/        pure-Go verifier (qcow2 via go-qcow2reader + go-diskfs) + ISO repack + qemu boot

ansible/
  main.yml                main local playbook entrypoint + post_tasks cleanup
  roles/                  network / system / packages / desktop / dev_tools / user

config/                   runtime configs (mise.toml, proxy-subscription.txt, haruna baseline)
dotfiles/                 stateless user dotfiles mirrored to $HOME via GNU Stow (--adopt)
scripts/                  bootstrap.sh, first-boot.service
```

The seed payload (what lands in the ISO's `/nocloud/` or the USB's `/nocloud/`) is: `first-boot.service`, `bootstrap.sh`, `fav.sh`, `config/`, `dotfiles/`, `ansible/`, plus `user-data`/`meta-data`.

## Hard-won facts (all validated in KVM, 2026-08)

- **grub.cfg MUST escape the `;`** in `ds=nocloud;s=/cdrom/nocloud/` as `\;`. Grub treats an unescaped `;` as a command separator and truncates the kernel cmdline, silently dropping the seed path. (This was a real-machine root cause of interactive fallback.)
- **user-data's first line must be EXACTLY `#cloud-config`** (no space). `# cloud-config` makes cloud-init treat it as unhandled user-data and drop the whole config.
- **The Ubuntu 26.04 desktop installer (ubuntu-desktop-bootstrap) does NOT apply `locale:`/`timezone:` to the target** — the Locale/TimeZone controllers are no-ops (verified via the subiquity debug log). The autoinstall config carries explicit late-commands to set them (locale.conf, locale-gen, /etc/localtime → Asia/Shanghai, tzdata debconf).
- **qemu `-kernel` direct boot does NOT trigger 26.04 desktop autoinstall** — it only engages through the ISO's EFI/grub path. The vmtest harness boots the repacked ISO via `-cdrom`.
- **Pure-Go no-root disk verification**: `github.com/lima-vm/go-qcow2reader` exposes a qcow2 as an `io.ReaderAt`; adapt it to `fs.File` for go-diskfs `OpenBackend` to read GPT + modern ext4 directly. Paths are io/fs-relative (`etc/passwd`, not `/etc/passwd`). go-diskfs does NOT read the qcow2 container itself — you must go through go-qcow2reader.
- **`xorriso -indev ISO -report_el_torito as_mkisofs`** gives the original's boot parameters; reuse them (strip the `'...'` quoting from interval paths — an absolute+quoted path fails to open). Use `-e '--interval:appended_partition_2:all::'` (symbolic), not the resolved form.
- **First-boot provision runs Before=systemd-user-sessions**, so dailyuser's session D-Bus isn't up — the fcitx5 phase degrades gracefully (env/autostart written; pinyin group may need a re-run after login).

## Key Design Decisions

- **Disk matching by serial** — autoinstall `match.serial` targets the exact system SSD; no match = installer halts, never touches the wrong disk.
- **Ansible modular roles** — post-install state managed declaratively with tags support (`pkg`, `desktop`, `dev`, `dotfiles`).
- **GNU Stow with `--adopt`** — seamlessly manages dotfile symlinks while resolving skel collisions.
- **Haruna physical copy** — stateful media player configs are isolated via physical seed copy to prevent local playback history from polluting the Git tree.
- **Self-disabling first-boot service** — `first-boot.service` is automatically disabled in `ansible/main.yml` `post_tasks` on successful delivery to prevent reboot delays.
- **No LUKS** — single-user desktop scope.
- **Password "1" + SDDM auto-login** — auto-login into KDE Plasma Wayland, sudo still needs the password.
