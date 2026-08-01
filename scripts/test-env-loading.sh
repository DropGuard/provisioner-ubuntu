#!/usr/bin/env bash
# test-env-loading.sh — verify that a non-interactive, non-login shell
# (the way an Agent or SSH `bash -c` runs) can find tools provisioned by
# mise / brew (e.g. java, node). This is the exact failure mode where
# `bash -c 'java --version'` returns "command not found" because neither
# .bashrc nor /etc/profile.d is sourced for non-interactive shells.
#
# What it tests:
#   1. A plain `sudo -u dailyuser -- bash -c '...'` finds java/node AFTER
#      the provisioner has written the environment files.
#   2. The mechanism that makes this work is one of:
#        - /etc/environment.d/zz-provisioner.conf (merged by systemd/PAM),
#        - BASH_ENV pointing at the profile.d snippet (non-interactive bash).
#
# Exit code: 0 = all checks pass, 1 = at least one tool missing.

set -uo pipefail

USER_NAME="${USER_NAME:-dailyuser}"

# Tools that mise/brew provision and an Agent would expect on PATH.
TOOLS=(java node mise brew)

fail=0

echo "=== Checking provisioned environment for user '${USER_NAME}' ==="
echo

# Simulate exactly what an Agent / remote shell does: a non-interactive
# non-login bash. We run it the same way the provisioner's run_user_fn does.
for tool in "${TOOLS[@]}"; do
    # `bash -c` with -l would read profile.d, but Agents usually do NOT pass
    # -l. Test the harder case: plain non-login, non-interactive.
    if sudo -u "${USER_NAME}" -- bash -c "command -v ${tool} >/dev/null 2>&1"; then
        path=$(sudo -u "${USER_NAME}" -- bash -c "command -v ${tool}" 2>/dev/null)
        printf "  [OK]   %-8s -> %s\n" "${tool}" "${path}"
    else
        # Try with login shell as a diagnostic — tells us if it's purely the
        # non-interactive gap or a deeper provisioning failure.
        if sudo -u "${USER_NAME}" -- bash -lc "command -v ${tool} >/dev/null 2>&1"; then
            printf "  [GAP]  %-8s found only with 'bash -lc' (non-interactive bash -c misses it)\n" "${tool}"
            fail=1
        else
            printf "  [MISS] %-8s not on PATH even via login shell (provisioning failed?)\n" "${tool}"
            fail=1
        fi
    fi
done

echo
if [[ ${fail} -eq 0 ]]; then
    echo "PASS: Agent-style 'bash -c' can load mise/brew environments."
    exit 0
else
    echo "FAIL: some tools are unreachable from a non-interactive shell."
    echo "      Fix: ensure /etc/environment.d and BASH_ENV cover non-login shells."
    exit 1
fi
