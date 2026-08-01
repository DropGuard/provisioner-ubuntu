#!/usr/bin/env bash
# analyze-install-log.sh — Judge whether the VM autoinstall actually engaged
# and whether target software got installed.
#
# Two stages:
#   1. Parse $WORK/console.log (serial console of the install) for proof that
#      cloud-init / subiquity picked up the nocloud seed (NOT interactive).
#   2. Mount the resulting qcow2 (requires qemu-nbd + nbd module) and inspect
#      /var/log/installer/ + dpkg to confirm packages from autoinstall.yaml
#      (openssh-server, firefox, code, etc.) are present.
#
# Must run as root (nbd mount). Requires: qemu-utils, nbd kernel module.
# Usage: sudo ./scripts/analyze-install-log.sh

set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORK="${VM_WORK:-$HOME/vmtest}"
LOG="$WORK/console.log"
QCOW="$WORK/target.qcow2"
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
fail=0
pass() { echo -e "  ${GREEN}PASS${NC}  $1"; }
fail() { echo -e "  ${RED}FAIL${NC}  $1"; fail=1; }
warn() { echo -e "  ${YELLOW}WARN${NC}  $1"; }

[[ "$(id -u)" -eq 0 ]] || { echo -e "${RED}ERROR:${NC} must run as root (sudo)."; exit 1; }
[[ -f "$LOG" ]] || { echo -e "${RED}ERROR:${NC} $LOG not found — run test-vm.sh first."; exit 1; }

echo "======================================================================"
echo " STAGE 1 — Did autoinstall engage? (serial console)"
echo "======================================================================"

# Strong positive signals that the seed was consumed:
grep -qE "Autoinstall|autoinstall" "$LOG" 2>/dev/null && pass "log mentions autoinstall" || fail "no 'autoinstall' mention in log"
grep -qiE "cloud-init|subiquity" "$LOG" 2>/dev/null && pass "cloud-init/subiquity ran" || warn "cloud-init/subiquity not clearly visible in log"

# NOTE: the definitive proof of unattended install is `interactive: false` +
# `state: ApplicationState.DONE` in the installed system's installer journal —
# verified in STAGE 2 below. A grub menu title or live-session "Welcome" text
# on the console is NOT a fallback signal (the 26.04 desktop installer boots
# the desktop behind the running install), so we do not grep for those here.

# Curtin/storage applying = it actually wrote to the target disk.
grep -qiE "curtin|Finished .* storage|Partitioning|Installing system" "$LOG" 2>/dev/null && pass "storage/partitioning step executed" || warn "storage step not clearly logged"

# Did it reach the end and reboot?
grep -qiE "reboot|finish|Installation complete|installed successfully" "$LOG" 2>/dev/null && pass "reached install completion / reboot" || warn "completion not clearly logged"

echo ""
echo "======================================================================"
echo " STAGE 2 — Were packages installed? (mount qcow2 and inspect)"
echo "======================================================================"

# Need nbd to mount a qcow2.
modprobe nbd 2>/dev/null || true
if ! command -v qemu-nbd >/dev/null; then
  warn "qemu-nbd not installed (apt-get install qemu-utils) — skipping on-disk inspection"
else
  [[ -f "$QCOW" ]] || { warn "$QCOW not found — skipping"; }
  if [[ -f "$QCOW" ]]; then
    qemu-nbd --connect=/dev/nbd0 "$QCOW" 2>/dev/null || warn "could not connect nbd0"
    if [[ -b /dev/nbd0p2 ]] || [[ -b /dev/nbd0p1 ]]; then
      MNT="$(mktemp -d)"
      # Try common root partitions; ext4 on p2 or p1.
      for p in /dev/nbd0p2 /dev/nbd0p1; do
        mount "$p" "$MNT" 2>/dev/null && break
      done
      if mountpoint -q "$MNT" 2>/dev/null; then
        INSTLOG="$MNT/var/log/installer"
        echo "  Mounted installed system at $MNT"

        # a) installer log present?
        [[ -d "$INSTLOG" ]] && pass "installer log dir present at /var/log/installer" \
          || warn "no /var/log/installer (install may not have completed)"

        # a2) THE unattended-install proof: the installer journal records
        # interactive:false + state DONE only when autoinstall fully engaged.
        JOURNAL="$INSTLOG/installer-journal.txt"
        if [[ -f "$JOURNAL" ]]; then
          if grep -q "interactive: false" "$JOURNAL" && grep -q "state: ApplicationState.DONE" "$JOURNAL"; then
            pass "installer ran unattended (interactive: false, state: DONE)"
          else
            fail "journal lacks 'interactive: false / state: DONE' — install may have fallen back to interactive"
          fi
        else
          warn "no installer-journal.txt — cannot confirm unattended mode"
        fi

        # b) key packages from autoinstall.yaml
        for pkg in openssh-server firefox code curl git build-essential; do
          if chroot "$MNT" dpkg -l "$pkg" 2>/dev/null | grep -qE "^ii"; then
            pass "package installed: $pkg"
          else
            warn "package NOT found: $pkg"
          fi
        done

        # c) user 'dailyuser' created (identity in autoinstall.yaml)
        if chroot "$MNT" id dailyuser >/dev/null 2>&1; then
          pass "user 'dailyuser' created"
        else
          warn "user 'dailyuser' not found"
        fi

        # d) ssh server enabled
        chroot "$MNT" systemctl is-enabled ssh >/dev/null 2>&1 && pass "ssh server enabled" || warn "ssh not enabled"

        umount "$MNT" 2>/dev/null; rmdir "$MNT" 2>/dev/null
      else
        warn "could not mount any partition from qcow2"
      fi
      qemu-nbd --disconnect /dev/nbd0 >/dev/null 2>&1 || true
    fi
  fi
fi

echo ""
echo "======================================================================"
if [[ $fail -eq 0 ]]; then
  echo -e "${GREEN}OVERALL: autoinstall engaged and system provisioned.${NC}"
  exit 0
else
  echo -e "${RED}OVERALL: autoinstall did NOT cleanly engage — see FAIL above.${NC}"
  exit 1
fi
