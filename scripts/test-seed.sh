#!/usr/bin/env bash
# test-seed.sh — Build the nocloud seed into a temp dir and assert the
# invariants that, if violated, make the installer silently fall back to the
# interactive path (the exact failure this project hit once).
#
# This exercises the REAL seed-building code path (copy-to-usb.py) end to end,
# so it catches regressions in either copy-to-usb.py OR prepare-usb.sh (both
# must produce an identical, valid seed).
#
# Usage:
#   ./scripts/test-seed.sh
#
# Exit 0 = all invariants hold. Exit 1 = at least one failed (prints which).

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
COPY_TO_USB="${SCRIPT_DIR}/copy-to-usb.py"

RED='\033[0;31m'; GREEN='\033[0;32m'; NC='\033[0m'

failures=0
pass()  { echo -e "  ${GREEN}PASS${NC}  $1"; }
fail()  { echo -e "  ${RED}FAIL${NC}  $1"; failures=$((failures+1)); }

# --- Build the seed into a throwaway USB-shaped dir ---
TMPUSB="$(mktemp -d)"
trap 'rm -rf "${TMPUSB}"' EXIT

# copy-to-usb.py guards on casper/vmlinuz presence; fake it so the guard passes.
mkdir -p "${TMPUSB}/casper"
touch "${TMPUSB}/casper/vmlinuz"

# Use a fabricated serial/password so we can assert substitution happened.
TEST_SERIAL="TESTSERIAL000000000"
TEST_PASS="testpass"

python3 "${COPY_TO_USB}" "${TMPUSB}" --serial "${TEST_SERIAL}" --password "${TEST_PASS}" >/dev/null 2>&1 \
  || { echo -e "${RED}ERROR${NC}: copy-to-usb.py failed to build the seed."; exit 1; }

SEED="${TMPUSB}/nocloud"

echo "Seed invariants:"
echo "----------------------------------------------------------------------"

# 1. Required files present
for f in user-data autoinstall.yaml meta-data; do
  if [[ -f "${SEED}/${f}" ]]; then pass "${f} present in seed";
  else fail "${f} MISSING from seed (installer will go interactive)"; fi
done

# 2. user-data and autoinstall.yaml are identical mirrors
if cmp -s "${SEED}/user-data" "${SEED}/autoinstall.yaml"; then
  pass "user-data is identical to autoinstall.yaml"
else
  fail "user-data and autoinstall.yaml DIVERGED"
fi

# 3. No unexpanded placeholders survive
ph=$(grep -c "__DISK_SERIAL__\|__USER_PASSWORD_HASH__" "${SEED}/user-data" 2>/dev/null || true)
if [[ "${ph}" -eq 0 ]]; then pass "no unexpanded placeholders in user-data";
else fail "${ph} unexpanded placeholder(s) left in user-data"; fi

# 4. Serial was actually substituted (not the literal placeholder, and matches input)
if grep -q "${TEST_SERIAL}" "${SEED}/user-data"; then pass "disk serial substituted (${TEST_SERIAL})";
else fail "disk serial NOT substituted into user-data"; fi

# 5. user-data is valid cloud-config + autoinstall structure
head -1 "${SEED}/user-data" | grep -q "^# cloud-config" \
  && pass "user-data has # cloud-config header" \
  || fail "user-data missing # cloud-config header"
grep -q "^autoinstall:" "${SEED}/user-data" \
  && pass "user-data declares autoinstall:" \
  || fail "user-data missing autoinstall: key"
grep -q "version: 1" "${SEED}/user-data" \
  && pass "autoinstall version: 1 present" \
  || fail "autoinstall version missing"

# 6. grub template carries the autoinstall directive (installer engages)
GRUB_TPL="${PROJECT_DIR}/autoinstall/grub.cfg"
if grep -q "autoinstall" "${GRUB_TPL}" && grep -q "ds=nocloud" "${GRUB_TPL}"; then
  pass "grub.cfg template carries 'autoinstall ds=nocloud'"
else
  fail "grub.cfg template missing autoinstall/ds=nocloud (installer won't autoinstall)"
fi

echo "----------------------------------------------------------------------"
if [[ "${failures}" -eq 0 ]]; then
  echo -e "${GREEN}ALL SEED INVARIANTS PASS${NC}"
  exit 0
else
  echo -e "${RED}${failures} INVARIANT(S) FAILED${NC}"
  exit 1
fi
