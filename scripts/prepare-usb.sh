#!/usr/bin/env bash
# prepare-usb.sh — Build a bootable Ubuntu autoinstall USB drive.
#
# Usage:
#   ./prepare-usb.sh \\
#     --iso ubuntu-24.04.1-desktop-amd64.iso \\
#     --disk /dev/sdb \\
#     --serial 50026B727200FDDC \\
#     --password 1
#
# What it does:
#   1. Formats the USB disk with a FAT32 partition
#   2. Extracts the Ubuntu ISO onto the USB
#   3. Copies autoinstall.yaml, meta-data, provision.sh, and config files
#      into the cloud-init seed partition so the installer picks them up
#   4. Replaces __DISK_SERIAL__ and __USER_PASSWORD_HASH__ in autoinstall.yaml
#      using sed + openssl passwd -6
#
# Requirements:
#   - rsync, parted, mkfs.fat, openssl
#   - The target disk will be COMPLETELY WIPED

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

usage() {
    cat << 'EOF'
Usage: prepare-usb.sh --iso <path> --disk <device> [--serial <disk-serial>] [--password <pw>]

Arguments:
  --iso <path>      Path to Ubuntu desktop ISO file
  --disk <device>   USB block device (e.g. /dev/sdb) — WILL BE WIPED
  --serial <id>     Target disk serial (from `lsblk -o SERIAL`). Defaults to
                    the known system SSD serial; override when building for
                    a different machine. If it doesn't match any disk, the
                    installer aborts instead of touching a wrong disk.
  --password <pw>   dailyuser password (default: 1)

Example:
  ./prepare-usb.sh \
    --iso ~/Downloads/ubuntu-24.04.1-desktop-amd64.iso \
    --disk /dev/sdb \
    --password 1
EOF
    exit 1
}

# --- Parse arguments ---
ISO=""
DISK=""
# Default to the known system SSD serial; override with --serial for other
# machines. Matched by the installer via storage.layout.match.serial.
DISK_SERIAL="50026B727200FDDC"
USER_PASSWORD="1"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --iso)      ISO="$2";      shift 2 ;;
        --disk)     DISK="$2";     shift 2 ;;
        --serial)   DISK_SERIAL="$2"; shift 2 ;;
        --password) USER_PASSWORD="$2"; shift 2 ;;
        -h|--help)  usage ;;
        *) echo -e "${RED}Unknown argument: $1${NC}"; usage ;;
    esac
done

if [[ -z "${ISO}" || -z "${DISK}" ]]; then
    echo -e "${RED}ERROR: --iso and --disk are required.${NC}"
    usage
fi

if [[ ! -f "${ISO}" ]]; then
    echo -e "${RED}ERROR: ISO file not found: ${ISO}${NC}"
    exit 1
fi

if [[ ! -b "${DISK}" ]]; then
    echo -e "${RED}ERROR: ${DISK} is not a block device.${NC}"
    exit 1
fi

# --- Safety checks ---
REQUIRED=(rsync parted mkfs.fat openssl)
for cmd in "${REQUIRED[@]}"; do
    if ! command -v "${cmd}" &>/dev/null; then
        echo -e "${RED}ERROR: '${cmd}' not found. Please install it first.${NC}"
        exit 1
    fi
done

if [[ "${EUID}" -ne 0 ]]; then
    echo -e "${RED}ERROR: must run as root (sudo).${NC}"
    exit 1
fi

# --- Find the project root ---
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

AUTOINSTALL_DIR="${PROJECT_DIR}/autoinstall"
CONFIG_DIR="${PROJECT_DIR}/config"
PROVISION_SH="${SCRIPT_DIR}/provision.sh"
FIRST_BOOT_SERVICE="${SCRIPT_DIR}/first-boot.service"
TEST_ENV_SH="${SCRIPT_DIR}/test-env-loading.sh"

# Verify we have everything we need.
if [[ ! -f "${AUTOINSTALL_DIR}/autoinstall.yaml" ]]; then
    echo -e "${RED}ERROR: autoinstall.yaml not found in ${AUTOINSTALL_DIR}${NC}"
    exit 1
fi

# --- Confirm with user ---
echo ""
echo -e "${YELLOW}============================================================${NC}"
echo -e "${YELLOW}  WARNING: This will DESTROY ALL DATA on ${DISK}${NC}"
echo -e "${YELLOW}============================================================${NC}"
echo ""
echo "  ISO:         ${ISO}"
echo "  USB disk:    ${DISK}"
echo "  Target disk: serial ${DISK_SERIAL}"
echo ""

read -rp "  Type 'YES' to proceed: " confirm
if [[ "${confirm}" != "YES" ]]; then
    echo "Aborted."
    exit 0
fi

# --- Partition the USB disk ---
echo ""
echo -e "${GREEN}[1/5] Partitioning ${DISK}...${NC}"

# Wipe partition table and create a single FAT32 partition.
parted -s "${DISK}" mklabel gpt
parted -s "${DISK}" mkpart primary fat32 0% 100%
parted -s "${DISK}" set 1 boot on

# Wait for the kernel to re-read the partition table.
sleep 2

PART="${DISK}1"
if [[ "${DISK}" == *"nvme"* ]]; then
    PART="${DISK}p1"
fi

echo -e "${GREEN}[2/5] Formatting ${PART} as FAT32...${NC}"
mkfs.fat -F 32 "${PART}"

# --- Mount ---
MNT="$(mktemp -d)"
mount "${PART}" "${MNT}"
trap 'umount "${MNT}"; rm -rf "${MNT}"' EXIT

# --- Extract ISO to USB ---
echo -e "${GREEN}[3/5] Extracting ISO to USB (this may take a few minutes)...${NC}"

ISO_MNT="$(mktemp -d)"
mount -o loop "${ISO}" "${ISO_MNT}"
trap 'umount "${ISO_MNT}"; rm -rf "${ISO_MNT}"; umount "${MNT}"; rm -rf "${MNT}"' EXIT

# Copy ISO contents to USB.
rsync -a --info=progress2 "${ISO_MNT}/" "${MNT}/"

# --- Prepare autoinstall seed ---
echo -e "${GREEN}[4/5] Preparing autoinstall seed...${NC}"

# cloud-init nocloud seed: files go into /nocloud/ at the USB root.
SEED="${MNT}/nocloud"
mkdir -p "${SEED}"

# Generate password hash.
PASSWORD_HASH=$(openssl passwd -6 "${USER_PASSWORD}")
echo "    Password hash generated."

# Copy autoinstall.yaml, replacing placeholders (| delimiter avoids
# escaping issues with / and $ in the password hash).
sed -e "s|__DISK_SERIAL__|${DISK_SERIAL}|g" \
    -e "s|__USER_PASSWORD_HASH__|${PASSWORD_HASH}|g" \
    "${AUTOINSTALL_DIR}/autoinstall.yaml" > "${SEED}/autoinstall.yaml"
# cloud-init's ds=nocloud mode reads the config from a file named
# 'user-data', NOT 'autoinstall.yaml'. Without it the installer silently
# falls back to the interactive path. Soalways mirror it.
cp "${SEED}/autoinstall.yaml" "${SEED}/user-data"
echo "    Serial set: ${DISK_SERIAL} (wrote autoinstall.yaml + user-data)"

cp "${AUTOINSTALL_DIR}/meta-data" "${SEED}/meta-data"

DOTFILES_DIR="${PROJECT_DIR}/dotfiles"
FAV_SH="${SCRIPT_DIR}/fav.sh"

# Copy provision script, config, dotfiles, and helper scripts.
cp "${PROVISION_SH}" "${SEED}/provision.sh"
cp "${FIRST_BOOT_SERVICE}" "${SEED}/first-boot.service"
cp "${TEST_ENV_SH}" "${SEED}/test-env-loading.sh"
[[ -f "${FAV_SH}" ]] && cp "${FAV_SH}" "${SEED}/fav.sh"
cp "${SCRIPT_DIR}/setup-fcitx5-chinese.sh" "${SEED}/setup-fcitx5-chinese.sh"
mkdir -p "${SEED}/config"
cp -a "${CONFIG_DIR}/." "${SEED}/config/" 2>/dev/null || true
if [[ -d "${DOTFILES_DIR}" ]]; then
    mkdir -p "${SEED}/dotfiles"
    cp -a "${DOTFILES_DIR}/." "${SEED}/dotfiles/" 2>/dev/null || true
fi

# --- Configure GRUB from the pinned template (not regex-patched) ---
echo -e "${GREEN}[5/5] Configuring GRUB for autoinstall...${NC}"

GRUB_TEMPLATE="${PROJECT_DIR}/autoinstall/grub.cfg"
GRUB_CFG="${MNT}/boot/grub/grub.cfg"

# Find the grub dir on the USB (variant location across ISO revisions).
if [[ ! -f "${GRUB_CFG}" ]]; then
    GRUB_CFG="${MNT}/EFI/BOOT/grub.cfg"
fi

if [[ -f "${GRUB_TEMPLATE}" && -d "$(dirname "${GRUB_CFG}")" ]]; then
    cp "${GRUB_TEMPLATE}" "${GRUB_CFG}"
    # Verify the autoinstall directive actually made it into the file.
    if grep -q "autoinstall" "${GRUB_CFG}" && grep -q "ds=nocloud" "${GRUB_CFG}"; then
        echo "    grub.cfg written from template (autoinstall + ds=nocloud)."
    else
        echo -e "${RED}    ERROR: template missing autoinstall/ds=nocloud — aborting.${NC}"
        exit 1
    fi
else
    echo -e "${YELLOW}    WARNING: grub template or grub dir missing —"
    echo -e "             add 'autoinstall ds=nocloud;s=/cdrom/nocloud/' to the"
    echo -e "             kernel cmdline in ${GRUB_CFG} manually.${NC}"
fi

# --- Done ---
echo ""
echo -e "${GREEN}============================================================${NC}"
echo -e "${GREEN}  USB drive prepared successfully!${NC}"
echo -e "${GREEN}============================================================${NC}"
echo ""
echo "  Next steps:"
echo "  1. Insert the USB drive into the target machine."
echo "  2. Boot from USB (select UEFI boot)."
echo "  3. The installer will run automatically — no interaction needed."
echo "  4. After reboot, log in as 'dailyuser' (password: 1)."
echo "  5. provision.sh will run on first login and set up everything."
echo ""

umount "${ISO_MNT}"
rm -rf "${ISO_MNT}"
umount "${MNT}"
rm -rf "${MNT}"
trap - EXIT
