#!/usr/bin/env bash
# test-vm.sh — Validate the autoinstall config end-to-end in a real KVM VM.
#
# The ONLY boot path that drives Ubuntu 26.04 desktop autoinstall is the ISO's
# own EFI/grub path (a qemu `-kernel` direct boot is NOT honoured by the
# desktop installer, no matter the cmdline). So this script:
#   1. builds the nocloud seed from autoinstall/* (placeholders substituted),
#   2. extracts the ISO, writes boot/grub/grub.cfg with the autoinstall kernel
#      line (the `ds=nocloud;s=/cdrom/nocloud/` semicolon MUST be escaped as
#      `\;` — grub truncates the kernel cmdline at an unescaped `;`), and
#      plants the seed at /nocloud/ on the ISO,
#   3. repacks the ISO with xorriso, deriving the boot parameters from the
#      ORIGINAL ISO itself (xorriso -indev ... -report_el_torito as_mkisofs)
#      so it works across point releases,
#   4. boots the repacked ISO via -cdrom (OVMF -> grub -> casper) and lets the
#      installer run unattended.
#
# The installed system lands in $WORK/target.qcow2 and the serial console in
# $WORK/console.log. Then run: sudo ./scripts/analyze-install-log.sh
#
# Must run as root (loop-mount extraction). Requires:
#   apt-get install qemu-system-x86 qemu-utils ovmf cloud-image-utils xorriso
#   + ~15 GB free in $WORK (ISO tree + repacked ISO + target disk).
#
# Usage:
#   sudo ./scripts/test-vm.sh [--iso ~/Downloads/ubuntu-26.04-desktop-amd64.iso] [--timeout 2400]
#
# Outputs:
#   $WORK/console.log        — serial console of the install
#   $WORK/target.qcow2       — installed system disk (for analyze-install-log.sh)
#   $WORK/ubuntu-autoinstall.iso — the repacked ISO (grub + seed baked in)

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

WORK="${VM_WORK:-$HOME/vmtest}"
ISO="${VM_ISO:-$HOME/Downloads/ubuntu-26.04-desktop-amd64.iso}"
TIMEOUT="${VM_TIMEOUT:-2400}"
MEM="${VM_MEM:-4096}"
SMP="${VM_SMP:-2}"
SERIAL="${VM_DISK_SERIAL:-50026B727200FDDC}"   # matches autoinstall.yaml match.serial
USER_PASS="${VM_USER_PASSWORD:-1}"
TARGET_QCOW2="${WORK}/target.qcow2"
REPACKED_ISO="${WORK}/ubuntu-autoinstall.iso"
TREE="${WORK}/iso-tree"
CONSOLE_LOG="${WORK}/console.log"
ISO_MNT="${WORK}/iso-mnt"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
die() { echo -e "${RED}ERROR:${NC} $*" >&2; exit 1; }
step() { echo -e "${GREEN}==>${NC} $*"; }

[[ "$(id -u)" -eq 0 ]] || die "must run as root (sudo)."
for cmd in qemu-system-x86_64 qemu-img cloud-localds xorriso rsync mount; do
  command -v "$cmd" >/dev/null || die "'$cmd' not found"
done

# --- Locate OVMF (UEFI needs pflash: CODE + VARS pair, not -bios) ---
OVMF_CODE="/usr/share/OVMF/OVMF_CODE_4M.fd"
OVMF_VARS="/usr/share/OVMF/OVMF_VARS_4M.fd"
[[ -f "$OVMF_CODE" && -f "$OVMF_VARS" ]] || die "OVMF firmware not found — apt-get install ovmf"
[[ -f "$ISO" ]] || die "ISO not found: $ISO (set VM_ISO=/path/to.iso)"

mkdir -p "$WORK"
step "Cleaning previous run artifacts in $WORK ..."
rm -rf "$TREE" "$ISO_MNT" "$TARGET_QCOW2" "$CONSOLE_LOG" \
       "${WORK}/OVMF_VARS_4M.fd" "${WORK}/user-data" "$REPACKED_ISO"
[[ -d "$TREE" ]] || mkdir -p "$TREE"

SRC_YAML="${PROJECT_DIR}/autoinstall/autoinstall.yaml"
META_DATA="${PROJECT_DIR}/autoinstall/meta-data"
[[ -f "$SRC_YAML" ]] || die "autoinstall/autoinstall.yaml not found"

# --- 1. Build the nocloud seed from autoinstall.yaml (mirrors prepare-usb.sh) ---
step "Building nocloud seed from autoinstall.yaml"
PHASH="$(openssl passwd -6 "$USER_PASS")" || die "openssl passwd failed"
sed -e "s|__DISK_SERIAL__|${SERIAL}|g" \
    -e "s|__USER_PASSWORD_HASH__|${PHASH}|g" \
    "$SRC_YAML" > "${WORK}/user-data"
if grep -q "__DISK_SERIAL__\|__USER_PASSWORD_HASH__" "${WORK}/user-data"; then
  die "placeholder substitution failed — check sed above"
fi

# --- 2. Extract ISO, patch grub, plant seed ---
step "Extracting ISO to $TREE (6.5 GB, takes a minute)"
mkdir -p "$ISO_MNT"
mount -o loop,ro "$ISO" "$ISO_MNT" || die "could not loop-mount ISO (need loop support)"
rsync -a "$ISO_MNT/" "$TREE/" || { umount "$ISO_MNT" 2>/dev/null; die "rsync failed"; }
umount "$ISO_MNT"; rmdir "$ISO_MNT" 2>/dev/null

chmod -R u+w "$TREE"

# grub.cfg with autoinstall + escaped semicolon + serial console for observability.
# NOTE: `\;` is required — grub splits the linux line at an unescaped `;`, which
# silently drops the s=/cdrom/nocloud/ seed path (root cause of interactive fallback).
cat > "$TREE/boot/grub/grub.cfg" <<'EOF'
set timeout=0

serial --unit=0 --speed=115200
terminal_input serial
terminal_output serial

menuentry "Ubuntu autoinstall" {
    linux  /casper/vmlinuz console=ttyS0 autoinstall ds=nocloud\;s=/cdrom/nocloud/ ---
    initrd /casper/initrd
}
menuentry "Ubuntu (safe graphics)" {
    linux  /casper/vmlinuz console=ttyS0 nomodeset ---
    initrd /casper/initrd
}
EOF

# Seed payload (user-data is what ds=nocloud reads — the mirror autoinstall.yaml
# is kept for parity with the real USB).
SEED="$TREE/nocloud"
mkdir -p "$SEED/config" "$SEED/dotfiles"
cp "${WORK}/user-data" "$SEED/user-data"
cp "${WORK}/user-data" "$SEED/autoinstall.yaml"
cp "${META_DATA}" "$SEED/meta-data"
cp "${SCRIPT_DIR}/provision.sh"        "$SEED/provision.sh"
cp "${SCRIPT_DIR}/first-boot.service"  "$SEED/first-boot.service"
cp "${SCRIPT_DIR}/test-env-loading.sh" "$SEED/test-env-loading.sh"
[[ -f "${SCRIPT_DIR}/fav.sh" ]] && cp "${SCRIPT_DIR}/fav.sh" "$SEED/fav.sh"
cp "${SCRIPT_DIR}/setup-fcitx5-chinese.sh" "$SEED/setup-fcitx5-chinese.sh"
cp -a "${PROJECT_DIR}/config/.".   "$SEED/config/" 2>/dev/null || true
[[ -d "${PROJECT_DIR}/dotfiles" ]] && cp -a "${PROJECT_DIR}/dotfiles/." "$SEED/dotfiles/" 2>/dev/null || true
# Drop a stale boot catalog — xorriso regenerates it via -c.
rm -f "$TREE/boot.catalog"

# --- 3. Repack the ISO, deriving boot params from the ORIGINAL ISO ---
step "Repacking ISO with autoinstall grub + seed (takes a few minutes)"
REPORT="$(xorriso -indev "$ISO" -report_el_torito as_mkisofs 2>&1 | sed -n '/^-V /,/^-o/p')"
# NOTE: use single-quoted grep patterns or `awk -F"'"` here — a double-quoted
# grep pattern containing a literal ' breaks bash parsing inside "$( ... )".
VOL="$(echo "$REPORT" | awk -F"'" '/^-V /{print $2; exit}')" || die "could not read volume label"
MOD="$(echo "$REPORT" | awk -F"'" '/^--modification-date=/{print $2; exit}')"
GRUB2="$(echo "$REPORT" | grep -oP '^--grub2-mbr --interval:local_fs:.*')"
APPPART="$(echo "$REPORT" | grep -oP '^-append_partition 2 .*')"
EIMG="$(echo "$REPORT" | awk -F"'" '/^-b /{print $2; exit}')"
EBLS="$(echo "$REPORT" | grep -oP '^\-boot-load-size \K\d+' | head -1)"
EFI_BLS="$(echo "$REPORT" | grep -oP '^\-boot-load-size \K\d+' | tail -1)"
ISO_MBR="$(echo "$REPORT" | grep -oP '^\-iso_mbr_part_type \K[0-9a-f]+')"
[[ -n "$GRUB2" && -n "$APPPART" && -n "$EFI_BLS" ]] || die "could not parse ISO boot parameters"

# shellcheck disable=SC2086 # deliberate: $GRUB2/$APPPART each expand to two tokens
xorriso -as mkisofs -r \
  -V "$VOL" \
  --modification-date="$MOD" \
  $GRUB2 \
  --protective-msdos-label \
  -partition_cyl_align off \
  -partition_offset 16 \
  --mbr-force-bootable \
  $APPPART \
  -appended_part_as_gpt \
  -iso_mbr_part_type "$ISO_MBR" \
  -c '/boot.catalog' \
  -b "$EIMG" \
  -no-emul-boot -boot-load-size "$EBLS" -boot-info-table \
  --grub2-boot-info \
  -eltorito-alt-boot \
  -e '--interval:appended_partition_2:all::' \
  -no-emul-boot -boot-load-size "$EFI_BLS" \
  -o "$REPACKED_ISO" "$TREE" >/dev/null 2>&1 || die "xorriso repack failed"
[[ -s "$REPACKED_ISO" ]] || die "repacked ISO empty"

# --- 4. Boot the repacked ISO via its normal EFI/grub path ---
step "Creating sparse target disk (serial=$SERIAL)"
qemu-img create -f qcow2 "$TARGET_QCOW2" 120G >/dev/null
cp "$OVMF_VARS" "${WORK}/OVMF_VARS_4M.fd"

echo -e "${YELLOW}============================================================${NC}"
echo "  VM autoinstall validation (repacked ISO, EFI/grub path)"
echo "  ISO:     $REPACKED_ISO"
echo "  Target:  $TARGET_QCOW2 (serial=$SERIAL)"
echo "  Timeout: ${TIMEOUT}s"
echo "  Console: $CONSOLE_LOG"
echo -e "${YELLOW}============================================================${NC}"
echo ""

timeout "$TIMEOUT" qemu-system-x86_64 \
  -machine q35,accel=kvm \
  -cpu host \
  -m "$MEM" -smp "$SMP" \
  -drive if=pflash,format=raw,readonly=on,file="$OVMF_CODE" \
  -drive if=pflash,format=raw,file="${WORK}/OVMF_VARS_4M.fd" \
  -boot order=d,menu=off \
  -cdrom "$REPACKED_ISO" \
  -drive file="$TARGET_QCOW2",format=qcow2,if=none,id=target \
  -device virtio-blk-pci,drive=target,serial="$SERIAL" \
  -netdev user,id=net0,hostfwd=tcp::2222-:22 \
  -device virtio-net-pci,netdev=net0 \
  -nographic -serial file:"$CONSOLE_LOG" -monitor none -no-reboot \
  2>&1 | tail -5

rc=$?
echo ""
if [[ $rc -eq 124 ]]; then
  echo -e "${YELLOW} Timed out after ${TIMEOUT}s — installer did not finish (check console.log).${NC}"
elif [[ $rc -eq 0 ]]; then
  echo -e "${GREEN} qemu exited (installer finished / rebooted). Target: $(du -h "$TARGET_QCOW2" 2>/dev/null | cut -f1)${NC}"
else
  echo -e "${YELLOW} qemu exited with code $rc.${NC}"
fi
echo ""
echo "Next: sudo ./scripts/analyze-install-log.sh"
