#!/usr/bin/env python3
"""Copy nocloud seed files onto an existing Ubuntu USB drive.

Run from WSL or Linux. Pass the USB mount point:

    python copy-to-usb.py /mnt/d       # WSL: D: drive
    python copy-to-usb.py /mnt/e       # WSL: E: drive
    python copy-to-usb.py /mnt/d --serial 50026B727200FDDC

If you don't know the drive letter, run:  ls /mnt/

--serial defaults to the known system SSD; override it when copying the seed
to a USB intended for a different machine. The installer matches the disk by
this serial (storage.layout.match), and aborts if no disk matches.
"""

import argparse
import os
import shutil
import subprocess
import sys
from pathlib import Path


def password_hash(password: str) -> str:
    try:
        proc = subprocess.run(
            ["openssl", "passwd", "-6", password],
            capture_output=True, text=True, check=True,
        )
        return proc.stdout.strip()
    except FileNotFoundError:
        try:
            proc = subprocess.run(
                ["wsl", "openssl", "passwd", "-6", password],
                capture_output=True, text=True, check=True,
            )
            return proc.stdout.strip()
        except FileNotFoundError:
            sys.exit("ERROR: 'openssl' is not installed, and WSL is not available. Please run this in WSL or install Git for Windows.")
        except subprocess.CalledProcessError as e:
            sys.exit(f"ERROR: WSL openssl failed: {e.stderr}")


def copy_bytes(src_path: Path, dst_path: Path) -> None:
    """Copy file contents only, no metadata.

    The target is a FAT/exFAT USB stick mounted via drvfs, which rejects
    POSIX metadata (utime/chmod) and makes shutil.copy2/copy fail with
    EPERM. The installer only needs file contents, so we stream bytes and
    touch nothing else.
    """
    dst_path.parent.mkdir(parents=True, exist_ok=True)
    dst_path.write_bytes(src_path.read_bytes())


def patch_grub(usb: Path, template: Path) -> None:
    """Overwrite the USB's grub.cfg with the project's pinned template.

    We copy a known-good, version-coupled template (extracted from the target
    Ubuntu ISO) instead of regex-patching the live file. This avoids silent
    failures: a regex that stops matching on an ISO revision would otherwise
    leave the disk un-autoinstalled with no error. The template pins the
    seed location explicitly (ds=nocloud;s=/cdrom/nocloud/) so cloud-init
    does not rely on auto-discovery.
    """
    if not template.exists():
        print(f"ERROR: grub template missing: {template}")
        print("  Re-extract it from the ISO you built the USB from.")
        sys.exit(1)

    for candidate in ["boot/grub/grub.cfg", "EFI/BOOT/grub.cfg"]:
        grub_cfg = usb / candidate
        if grub_cfg.parent.exists():
            break
    else:
        print("WARNING: no grub dir on USB — copy the template manually:")
        print(f"  {template} -> {usb}/boot/grub/grub.cfg")
        return

    copy_bytes(template, grub_cfg)

    # Verify the written file actually contains our autoinstall directive,
    # so a stale/short template cannot pass silently.
    written = grub_cfg.read_text(encoding="utf-8", errors="ignore")
    if "autoinstall" in written and "ds=nocloud" in written:
        print("  grub.cfg written from template (autoinstall + ds=nocloud verified).")
    else:
        print("ERROR: written grub.cfg is missing autoinstall/ds=nocloud — abort.")
        sys.exit(1)


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Copy nocloud seed files onto an existing Ubuntu USB drive."
    )
    parser.add_argument(
        "usb_mount",
        type=Path,
        help="Path to USB mount point (e.g. /mnt/d)",
    )
    parser.add_argument(
        "--serial",
        default="50026B727200FDDC",
        help="Target disk serial (default: 50026B727200FDDC)",
    )
    parser.add_argument(
        "--password",
        default=os.environ.get("USER_PASSWORD", "1"),
        help="Password for dailyuser (default: 1 or $USER_PASSWORD)",
    )
    args = parser.parse_args()

    usb = args.usb_mount.resolve()
    serial = args.serial

    if not (usb / "casper" / "vmlinuz").exists():
        sys.exit(f"{usb} doesn't look like an Ubuntu USB (no casper/vmlinuz).")

    # --- Source directory (project root, one level above this script) ---
    src = Path(__file__).resolve().parent.parent
    seed = usb / "nocloud"

    # --- Password hash ---
    pw = args.password
    phash = password_hash(pw)

    # --- Copy files ---
    (seed / "config").mkdir(parents=True, exist_ok=True)
    (seed / "dotfiles").mkdir(parents=True, exist_ok=True)

    yaml = (src / "autoinstall" / "autoinstall.yaml").read_text(encoding="utf-8")
    yaml = yaml.replace("__USER_PASSWORD_HASH__", phash)
    yaml = yaml.replace("__DISK_SERIAL__", serial)
    (seed / "autoinstall.yaml").write_text(yaml, encoding="utf-8")
    # cloud-init's ds=nocloud mode reads the config from 'user-data', not
    # 'autoinstall.yaml'. Mirror it so the installer doesn't fall back to
    # the interactive path.
    (seed / "user-data").write_text(yaml, encoding="utf-8")
    print(f"  Target disk serial: {serial}")

    # Copy raw bytes only (see copy_bytes for why).
    copy_bytes(src / "autoinstall" / "meta-data",       seed / "meta-data")
    copy_bytes(src / "scripts"   / "provision.sh",       seed / "provision.sh")
    copy_bytes(src / "scripts"   / "first-boot.service", seed / "first-boot.service")
    copy_bytes(src / "scripts"   / "test-env-loading.sh", seed / "test-env-loading.sh")
    
    fav_sh = src / "scripts" / "fav.sh"
    if fav_sh.is_file():
        copy_bytes(fav_sh, seed / "fav.sh")
        
    for f in (src / "config").glob("*"):
        if f.is_file():
            copy_bytes(f, seed / "config" / f.name)

    dotfiles_src = src / "dotfiles"
    if dotfiles_src.is_dir():
        for f in dotfiles_src.rglob("*"):
            if f.is_file():
                rel = f.relative_to(dotfiles_src)
                target = seed / "dotfiles" / rel
                target.parent.mkdir(parents=True, exist_ok=True)
                copy_bytes(f, target)

    for item in sorted(seed.rglob("*")):
        if item.is_file():
            print(f"  {item.relative_to(seed)}")

    # --- Patch GRUB (overwrite with pinned template + verify) ---
    patch_grub(usb, src / "autoinstall" / "grub.cfg")

    print()
    print("Done. Eject, boot in UEFI mode, walk away.")
    print(f"Login: dailyuser / password: {pw}")


if __name__ == "__main__":
    main()
