#!/usr/bin/env bash
# provision.sh — Ubuntu post-install provisioning script.
#
# Runs once on first boot via first-boot.service, then disables itself.
# Safe to re-run manually: every operation checks "is it already done?"
# before acting.
#
# Config files live alongside this script in
# /usr/local/share/provisioner-ubuntu/config/

set -euo pipefail

# ---------------------------------------------------------------------------
# Config
# ---------------------------------------------------------------------------

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
readonly CONFIG_DIR="${SCRIPT_DIR}/../share/provisioner-ubuntu/config"
readonly BREW_PREFIX="/home/linuxbrew/.linuxbrew"
readonly USER_NAME="dailyuser"
readonly USER_HOME="/home/${USER_NAME}"

# Accumulated warnings for the final summary.
declare -a WARNINGS=()
declare -a SKIPPED=()
START_TIME=$(date +%s)
readonly START_TIME

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

banner() {
    echo ""
    echo "==== $(date '+%H:%M:%S')  $* ===="
}

warn() {
    local msg="$*"
    echo "    [WARN] ${msg}" >&2
    WARNINGS+=("${msg}")
}

skip() {
    local msg="$*"
    echo "    [SKIP] ${msg}"
    SKIPPED+=("${msg}")
}

ok() {
    echo "    [OK] $*"
}

# Run a command as dailyuser via sudo. Use this for everything that should
# be owned by the daily user (brew, mise, npm, pip, etc.).
run_user() {
    sudo -u "${USER_NAME}" -- "$@"
}

# run_user_fn runs a named function as dailyuser in a login shell.
# The function body is serialized with `declare -f` and executed remotely,
# so there is no string-escaping of $() — the function is real bash code
# that can be tested by sourcing this script.
#
# Usage:
#   my_setup() { mise activate; node --version; }
#   run_user_fn my_setup
run_user_fn() {
    local fn_name="$1"
    # Serialize the function and call it. `declare -f` prints the function
    # definition; we then invoke it by name.
    sudo -u "${USER_NAME}" -- bash -l -c "$(declare -f "${fn_name}"); ${fn_name}"
}

# Read non-comment, non-empty lines from a file.
read_list() {
    local file="$1"
    if [[ ! -f "${file}" ]]; then
        return 0
    fi
    grep -v '^\s*#' "${file}" | grep -v '^\s*$' || true
}

# Print elapsed time.
elapsed() {
    local now
    now=$(date +%s)
    local dt=$((now - START_TIME))
    printf '%dm%ds' $((dt / 60)) $((dt % 60))
}

# Fetch from GitHub API, using a token if available to avoid rate limits.
github_api_curl() {
    local url="$1"
    local token_file="${CONFIG_DIR}/github_token.txt"
    if [[ -f "${token_file}" ]]; then
        # Read the token and strip any whitespace/newlines
        local token
        token=$(tr -d '[:space:]' < "${token_file}")
        if [[ -n "${token}" ]]; then
            curl -sSL -H "Authorization: Bearer ${token}" "${url}"
            return
        fi
    fi
    # Fallback to unauthenticated request
    curl -sSL "${url}"
}

# ---------------------------------------------------------------------------
# Phase 0 — Base environment
# ---------------------------------------------------------------------------

phase_00_apt_update() {
    banner "Phase 0 — Base environment: apt update + upgrade"
    
    # Add third-party repos before update
    # GitHub CLI
    if [[ ! -f /etc/apt/sources.list.d/github-cli.list ]]; then
        mkdir -p /usr/share/keyrings
        curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
            -o /usr/share/keyrings/githubcli-archive-keyring.gpg
        echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
            > /etc/apt/sources.list.d/github-cli.list
    fi

    # Enpass
    if [[ ! -f /etc/apt/sources.list.d/enpass.list ]]; then
        echo "deb https://apt.enpass.io/ stable main" \
            > /etc/apt/sources.list.d/enpass.list
        curl -fsSL https://apt.enpass.io/keys/enpass-linux.key \
            -o /etc/apt/trusted.gpg.d/enpass.asc
    fi

    apt-get update -qq
    apt-get upgrade -y -qq
    ok "apt update & upgrade complete (incl. 3rd party repos)"
}

phase_01_core_packages() {
    banner "Phase 0 — Installing core apt packages"

    local core=()
    local extra=()

    while IFS= read -r pkg; do
        case "${pkg}" in
            build-essential|curl|git|docker.io)
                core+=("${pkg}") ;;
            *)
                extra+=("${pkg}") ;;
        esac
    done < <(read_list "${CONFIG_DIR}/apt-packages.list")

    # Core packages — fail on error.
    echo "    Installing core: ${core[*]}"
    DEBIAN_FRONTEND=noninteractive apt-get install -y -qq "${core[@]}"
    ok "core packages installed"

    # Extra packages — best-effort.
    if [[ ${#extra[@]} -gt 0 ]]; then
        echo "    Installing extras: ${extra[*]}"
        DEBIAN_FRONTEND=noninteractive apt-get install -y -qq "${extra[@]}" || {
            warn "some extra apt packages failed to install"
        }
        ok "extra packages done"
    fi
}

phase_02_docker() {
    banner "Phase 0 — Docker setup"

    if groups "${USER_NAME}" | grep -q docker; then
        skip "docker group already set"
        return
    fi
    usermod -aG docker "${USER_NAME}"
    ok "added ${USER_NAME} to docker group"
}

phase_03_gpu_drivers() {
    banner "Phase 0 — GPU drivers (NVIDIA only)"

    # AMD/Intel use in-kernel drivers (amdgpu/i915) — nothing to do.
    if ! lspci | grep -qi nvidia; then
        skip "no NVIDIA GPU detected — AMD/Intel need no driver"
        return
    fi

    # Ubuntu's "driver manager": installs the recommended NVIDIA driver.
    # Best-effort: failure is a warning (a bad driver is worse than none).
    if ! ubuntu-drivers autoinstall; then
        warn "ubuntu-drivers autoinstall failed — check NVIDIA driver manually"
    else
        ok "NVIDIA driver installed (reboot to activate)"
    fi
}


phase_05_cc_switch() {
    banner "Phase 0 — cc-switch"

    if command -v cc-switch &>/dev/null; then
        skip "cc-switch already installed"
        return
    fi

    local arch
    arch=$(dpkg --print-architecture)
    case "${arch}" in
        amd64) arch="x86_64" ;;
        arm64) arch="arm64" ;;
        *) warn "unsupported arch for cc-switch: ${arch}"; return ;;
    esac

    local url
    url=$(github_api_curl "https://api.github.com/repos/farion1231/cc-switch/releases/latest" \
        | jq -r '.assets[] | select(.browser_download_url | endswith("Linux-'"${arch}"'.deb")) | .browser_download_url' \
        | head -1)

    if [[ -z "${url}" || "${url}" == "null" ]]; then
        warn "cc-switch: could not find release asset for ${arch}"
        return
    fi

    curl -fsSL "${url}" -o /tmp/cc-switch.deb
    apt install -y -qq /tmp/cc-switch.deb
    rm -f /tmp/cc-switch.deb
    ok "cc-switch installed"
}


phase_07_clash_verge() {
    banner "Phase 0 — clash-verge-rev"

    if command -v clash-verge &>/dev/null; then
        skip "clash-verge already installed"
        return
    fi

    # Fetch latest deb URL from Github API
    local url
    url=$(github_api_curl "https://api.github.com/repos/clash-verge-rev/clash-verge-rev/releases/latest" \
        | jq -r '.assets[] | select(.name | test(".*_amd64\\.deb$")) | .browser_download_url' \
        | head -1)

    if [[ -z "${url}" || "${url}" == "null" ]]; then
        warn "clash-verge-rev: could not find release asset for amd64"
        return
    fi

    curl -fsSL "${url}" -o /tmp/clash-verge.deb
    apt install -y -qq /tmp/clash-verge.deb
    rm -f /tmp/clash-verge.deb

    # Install the systemd service for TUN mode
    if [[ -x /usr/bin/clash-verge-service-install ]]; then
        /usr/bin/clash-verge-service-install || warn "failed to install clash-verge TUN service"
    fi

    ok "clash-verge-rev installed and service setup"
}

# ---------------------------------------------------------------------------
# Phase 1 — Dev toolchain
# ---------------------------------------------------------------------------

phase_10_homebrew() {
    banner "Phase 1 — Homebrew"

    if [[ -x "${BREW_PREFIX}/bin/brew" ]]; then
        skip "brew already installed"
        return
    fi

    run_user_fn install_homebrew
    ok "Homebrew installed"
}

# install_homebrew runs as dailyuser in a login shell.
install_homebrew() {
    NONINTERACTIVE=1 /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
}

phase_11_mise() {
    banner "Phase 1 — mise (language version manager)"

    if run_user test -x "${USER_HOME}/.local/bin/mise"; then
        skip "mise already installed"
        return
    fi

    run_user_fn install_mise
    ok "mise installed"
}

# install_mise runs as dailyuser in a login shell.
install_mise() {
    curl -fsSL https://mise.run | sh
}

phase_12_brew_packages() {
    banner "Phase 1 — brew packages"

    local brew="${BREW_PREFIX}/bin/brew"
    if [[ ! -x "${brew}" ]]; then
        warn "brew not found, skipping brew packages"
        return
    fi

    while IFS= read -r pkg; do
        if run_user "${brew}" list --formula "${pkg}" &>/dev/null; then
            skip "brew ${pkg} already installed"
            continue
        fi
        echo "    Installing brew: ${pkg}"
        run_user "${brew}" install "${pkg}" || warn "brew install ${pkg} failed"
    done < <(read_list "${CONFIG_DIR}/brew-packages.list")
    ok "brew packages done"
}

phase_13_mise_tools() {
    banner "Phase 1 — mise tools & language runtimes"

    local mise="${USER_HOME}/.local/bin/mise"
    if [[ ! -x "${mise}" ]]; then
        warn "mise not found, skipping"
        return
    fi

    local cfg="${CONFIG_DIR}/mise.toml"
    if [[ ! -f "${cfg}" ]]; then
        warn "mise.toml not found at ${cfg}"
        return
    fi

    # Copy mise config to user home so `mise install` can read it.
    cp "${cfg}" "${USER_HOME}/.config/mise/config.toml"
    chown "${USER_NAME}:${USER_NAME}" "${USER_HOME}/.config/mise/config.toml"

    echo "    Installing mise tools (this may take a while)..."
    run_user_fn mise_install_tools || {
        warn "mise install had failures — check individual tools above"
    }
    ok "mise tools done"
}

# mise_install_tools runs as dailyuser in a login shell.
mise_install_tools() {
    eval "$("$HOME/.local/bin/mise" activate bash)"
    mise install
}

# ---------------------------------------------------------------------------
# Phase 2 — CLI tools
# ---------------------------------------------------------------------------

phase_20_npm_globals() {
    banner "Phase 2 — npm global packages"

    # reasonix needs node, which mise provides.
    run_user_fn npm_install_reasonix || {
        warn "npm install -g reasonix failed"
    }
    ok "npm globals done"
}

# npm_install_reasonix runs as dailyuser in a login shell.
npm_install_reasonix() {
    eval "$("$HOME/.local/bin/mise" activate bash)"
    npm install -g reasonix
}

phase_21_opencode() {
    banner "Phase 2 — opencode"

    if run_user test -x "${USER_HOME}/.local/bin/opencode"; then
        skip "opencode already installed"
        return
    fi

    run_user_fn install_opencode || {
        warn "opencode install failed"
    }
    ok "opencode installed"
}

# install_opencode runs as dailyuser in a login shell.
install_opencode() {
    curl -fsSL https://opencode.ai/install | bash
}

phase_22_claude_code() {
    banner "Phase 2 — Claude Code"

    if run_user test -x "${USER_HOME}/.local/bin/claude"; then
        skip "Claude Code already installed"
        return
    fi

    run_user_fn install_claude_code || {
        warn "Claude Code install failed"
    }
    ok "Claude Code installed"
}

# install_claude_code runs as dailyuser in a login shell.
install_claude_code() {
    curl -fsSL https://claude.ai/install.sh | bash
}

# ---------------------------------------------------------------------------
# Phase 3 — GNOME desktop
# ---------------------------------------------------------------------------

phase_30_gnome_theme() {
    banner "Phase 3 — GNOME desktop: dark theme"

    # Set dark mode preference.
    run_user_fn gnome_set_theme || {
        warn "failed to set dark theme"
    }
    ok "dark theme configured"
}

# gnome_set_theme runs as dailyuser in a login shell.
gnome_set_theme() {
    gsettings set org.gnome.desktop.interface color-scheme prefer-dark
    gsettings set org.gnome.desktop.interface gtk-theme Yaru-dark
}

phase_31_gnome_dock() {
    banner "Phase 3 — GNOME desktop: dock favorites"

    run_user_fn gnome_set_dock || {
        warn "failed to set dock favorites"
    }
    ok "dock favorites configured"
}

# gnome_set_dock runs as dailyuser in a login shell.
gnome_set_dock() {
    local favorites="[
        'firefox_firefox.desktop',
        'code_code.desktop',
        'org.gnome.Terminal.desktop',
        'org.gnome.Nautilus.desktop',
        'obsidian_obsidian.desktop',
        'discord_discord.desktop',
        'spotify_spotify.desktop'
    ]"
    gsettings set org.gnome.shell favorite-apps "$favorites"
}

phase_32_gnome_shortcuts() {
    banner "Phase 3 — GNOME desktop: keyboard shortcuts"

    # Terminal: Super+Return
    run_user_fn gnome_set_shortcuts || true
    ok "keyboard shortcuts configured"
}

# gnome_set_shortcuts runs as dailyuser in a login shell.
gnome_set_shortcuts() {
    gsettings set org.gnome.settings-daemon.plugins.media-keys custom-keybindings \
        "['/org/gnome/settings-daemon/plugins/media-keys/custom-keybindings/custom0/']"
    gsettings set org.gnome.settings-daemon.plugins.media-keys.custom-keybinding:/org/gnome/settings-daemon/plugins/media-keys/custom-keybindings/custom0/ name "Terminal"
    gsettings set org.gnome.settings-daemon.plugins.media-keys.custom-keybinding:/org/gnome/settings-daemon/plugins/media-keys/custom-keybindings/custom0/ command "gnome-terminal"
    gsettings set org.gnome.settings-daemon.plugins.media-keys.custom-keybinding:/org/gnome/settings-daemon/plugins/media-keys/custom-keybindings/custom0/ binding "<Super>Return"
}


phase_33_fcitx5_chinese() {
    banner "Phase 3 — GNOME desktop: fcitx5 Chinese input"

    # Root: install the packages. The user-side script then skips its own
    # `sudo apt-get` (fcitx5 is already present) and only writes the user
    # config — so it never prompts for the dailyuser password.
    if ! command -v fcitx5 >/dev/null; then
        apt-get install -y fcitx5 fcitx5-config-gui fcitx5-chinese-addons \
            fcitx5-frontend-gtk3 fcitx5-frontend-gtk2 fcitx5-frontend-gtk4 \
            fcitx5-frontend-qt5 fcitx5-frontend-qt6 || warn "fcitx5 apt install failed"
    else
        skip "fcitx5 packages already installed"
    fi

    # User: environment.d + autostart + D-Bus pinyin config, as dailyuser.
    # NOTE: first-boot.service runs Before=systemd-user-sessions, so dailyuser's
    # session D-Bus may not be up yet. The script degrades gracefully: env.d +
    # autostart are always written; the pinyin group config may need a re-run
    # after login (or fcitx5-configtool) if the D-Bus step can't connect.
    if [[ -x "${SCRIPT_DIR}/setup-fcitx5-chinese.sh" ]]; then
        if run_user "${SCRIPT_DIR}/setup-fcitx5-chinese.sh"; then
            ok "fcitx5 user config done"
        else
            warn "fcitx5 user config step failed (session D-Bus may not be ready yet — re-run setup-fcitx5-chinese.sh after login)"
        fi
    else
        warn "setup-fcitx5-chinese.sh not found next to provision.sh"
    fi
}


# ---------------------------------------------------------------------------
# Phase 4 — Shell environment
# ---------------------------------------------------------------------------

phase_40_shell_env() {
    banner "Phase 4 — Shell environment"

    # Two layers cover every way a tool gets launched:
    #
    #   1. .bashrc          — interactive shells (your open terminal).
    #   2. /etc/environment.d — systemd merges this into the environment of
    #                          every unit AND, via pam_systemd, into login
    #                          and SSH sessions. This is what makes tools
    #                          visible to GUI apps and to shells spawned by
    #                          an Agent — which run as non-interactive
    #                          `bash -c` and therefore DO NOT read .bashrc
    #                          or /etc/profile.d.
    #
    # We deliberately do NOT write /etc/profile.d: for a non-login,
    # non-interactive `bash -c` (the Agent case) it is not sourced either,
    # so it would only duplicate layer 1 without fixing the Agent gap.
    # Layer 2 (environment.d) is the one that actually closes that gap.

    # --- .bashrc (interactive shells) ---
    local bashrc="${USER_HOME}/.bashrc"
    if ! grep -q 'brew shellenv' "${bashrc}" 2>/dev/null; then
        # Quoted heredoc writes these lines verbatim into .bashrc so the
        # user's future interactive shells expand them. The literal
        # 'eval "$(...)"' is intentional — not a shellcheck mistake.
        cat >> "${bashrc}" << 'PROVISIONER_EOF'

# >>> provisioner-ubuntu: brew & mise activation <<<
eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"
eval "$($HOME/.local/bin/mise activate bash)"

# >>> Proxy toggles <<<
alias proxy='export http_proxy="http://127.0.0.1:7897" https_proxy="http://127.0.0.1:7897" all_proxy="socks5://127.0.0.1:7897"; echo "Terminal proxy ON"'
alias unproxy='unset http_proxy https_proxy all_proxy; echo "Terminal proxy OFF"'
PROVISIONER_EOF
        ok "added brew + mise activation to .bashrc"
    else
        skip ".bashrc already has brew activation"
    fi
    chown "${USER_NAME}:${USER_NAME}" "${bashrc}"

    # --- /etc/environment.d (global: GUI + SSH/Agent sessions) ---
    # This is the critical layer for Agents: their `bash -c` does not read
    # .bashrc or profile.d, but it DOES inherit the systemd/PAM environment.
    # Note: environment.d does not expand $PATH or $HOME, so the full PATH
    # must be spelled out. Keep in sync if the base PATH changes.
    local envd="/etc/environment.d/zz-provisioner.conf"
    if [[ ! -f "${envd}" ]]; then
        mkdir -p /etc/environment.d
        cat > "${envd}" << 'EOF'
PATH="/home/linuxbrew/.linuxbrew/bin:/home/dailyuser/.local/bin:/home/dailyuser/.local/share/mise/shims:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/snap/bin"
EOF
        chmod 644 "${envd}"
        ok "wrote ${envd} (global PATH via systemd/PAM)"
    else
        skip "${envd} already exists"
    fi
}


phase_41_git_config() {
    banner "Phase 4 — Shell environment: git config"

    # Git user identity — read from environment variables only.
    # Set GIT_USER_NAME and GIT_USER_EMAIL before running if you want
    # git configured. If they're not set, we skip this step gracefully
    # (no interactive prompt — this runs from a systemd service).
    local git_name="${GIT_USER_NAME:-}"
    local git_email="${GIT_USER_EMAIL:-}"

    if [[ -z "${git_name}" && -z "${git_email}" ]]; then
        skip "GIT_USER_NAME/GIT_USER_EMAIL not set — run 'provision' interactively later to configure git"
        # Still set sensible defaults that don't need identity.
        run_user git config --global init.defaultBranch main 2>/dev/null || true
        run_user git config --global pull.rebase false 2>/dev/null || true
        return
    fi

    if [[ -n "${git_name}" ]]; then
        run_user git config --global user.name "${git_name}"
        ok "git user.name = ${git_name}"
    fi
    if [[ -n "${git_email}" ]]; then
        run_user git config --global user.email "${git_email}"
        ok "git user.email = ${git_email}"
    fi

    # Sensible defaults.
    run_user git config --global init.defaultBranch main 2>/dev/null || true
    run_user git config --global pull.rebase false 2>/dev/null || true
    ok "git config done"
}

phase_42_dotfiles() {
    banner "Phase 4 — Shell environment: dotfiles"

    local dotfiles_dir="${SCRIPT_DIR}/../share/provisioner-ubuntu/dotfiles"
    if [[ ! -d "${dotfiles_dir}" ]] || [[ -z "$(ls -A "${dotfiles_dir}" 2>/dev/null)" ]]; then
        skip "no dotfiles directory found"
        return
    fi

    for src in "${dotfiles_dir}"/.* "${dotfiles_dir}"/*; do
        local base
        base=$(basename "${src}")
        # Skip . and ..
        [[ "${base}" == "." || "${base}" == ".." ]] && continue

        local dst="${USER_HOME}/${base}"
        if [[ -e "${dst}" ]]; then
            skip "dotfile ${base} already exists"
            continue
        fi
        cp -r "${src}" "${dst}"
        chown -R "${USER_NAME}:${USER_NAME}" "${dst}"
        ok "installed dotfile: ${base}"
    done
    ok "dotfiles done"
}

phase_43_mount_data_disks() {
    banner "Phase 4 — Mounting data disks (non-destructive)"

    # Goal: make the user's existing data disks available under /mnt after
    # the Windows->Ubuntu switch, WITHOUT touching the system disk or any
    # disk we were told to leave alone.
    #
    # Safety model (this is the whole point of the phase):
    #   * Never format, never write a partition table, never mkfs.
    #   * Only mount disks whose serial is NOT in the hard exclude list.
    #   * The system disk serial is the same one autoinstall matched on;
    #     the near-dead NVMe is excluded on purpose.
    #   * Mount by UUID (not /dev/sdX, which is unstable across boots).
    #   * `nofail` so a missing disk never blocks boot.
    #
    # If a disk has no recognisable filesystem, we skip it (with a warning)
    # rather than formatting it.

    # --- Hard excludes: serials we must NEVER touch ---
    # System disk (matched by autoinstall) and the failing NVMe.
    local -A EXCLUDE=(
        ["50026B727200FDDC"]=1   # KINGSTON 120G system disk
        ["38F6_0156_326B_257E"]=1 # PLEXTOR 1T NVMe — near-dead, leave alone
    )

    local uid gid
    uid=$(id -u "${USER_NAME}")
    gid=$(id -g "${USER_NAME}")

    # Discover disks. lsblk -d = top-level disks only (no partitions).
    local name serial fstype
    while read -r name serial fstype; do
        [[ -z "${name}" ]] && continue
        # Skip our hard excludes.
        if [[ -n "${EXCLUDE[${serial}]:-}" ]]; then
            skip "disk ${name} (serial ${serial}): excluded by policy"
            continue
        fi
        # Skip disks that have no filesystem anywhere yet (uninitialised).
        # We look at the disk-level FSTYPE first; if empty, scan partitions.
        local part_path=""
        if [[ -z "${fstype}" ]]; then
            # Find first partition with a filesystem on this disk.
            part_path=$(lsblk -r -n -o PATH,FSTYPE "/dev/${name}" 2>/dev/null \
                | awk '$2 != "" {print $1; exit}')
            [[ -z "${part_path}" ]] && {
                warn "disk ${name} (serial ${serial}): no filesystem found, skipped (not formatted)"
                continue
            }
        else
            part_path="/dev/${name}"
        fi

        # Get the filesystem UUID and type of the chosen partition.
        local puuid ptype
        read -r puuid ptype < <(blkid -o export "${part_path}" 2>/dev/null \
            | awk -F= '/^UUID=/{u=$2} /^TYPE=/{t=$2} END{print u, t}')

        [[ -z "${puuid}" ]] && {
            warn "disk ${name} (serial ${serial}): no UUID on ${part_path}, skipped"
            continue
        }

        # Idempotency: already in fstab?
        if grep -q "UUID=${puuid}" /etc/fstab 2>/dev/null; then
            skip "UUID ${puuid} (${name}) already in fstab"
            continue
        fi

        # Mount point labelled by serial tail so it is stable & identifiable.
        local label="${serial:-${name}}"
        label="${label//[^A-Za-z0-9]/_}"
        local mnt="/mnt/data-${label: -8}"
        mkdir -p "${mnt}"

        # NTFS needs ntfs3 + ownership mapping so dailyuser can read/write.
        # Everything else (ext4/exfat) mounts with defaults.
        local opts
        if [[ "${ptype}" == "ntfs" ]]; then
            opts="defaults,nofail,uid=${uid},gid=${gid},umask=022,windows_names,x-systemd.automount"
        else
            opts="defaults,nofail,uid=${uid},gid=${gid},x-systemd.automount"
        fi

        # Try mounting now (so we catch errors immediately, not at next boot).
        if mount -t "${ptype}" -U "${puuid}" "${mnt}" 2>/dev/null \
           || mount -U "${puuid}" "${mnt}" 2>/dev/null; then
            ok "mounted ${ptype} ${part_path} -> ${mnt}"
        else
            warn "could not mount ${part_path} (${ptype}); added to fstab anyway for manual retry"
        fi

        # Persist to fstab.
        printf 'UUID=%s %s %s %s 0 0\n' "${puuid}" "${mnt}" "${ptype}" "${opts}" >> /etc/fstab
        ok "fstab entry added for ${label} (${ptype}, UUID ${puuid})"
    done < <(lsblk -d -r -n -o NAME,SERIAL,FSTYPE 2>/dev/null)

    ok "data disk mount phase done (review /etc/fstab and /mnt)"
}

phase_50_disable_service() {
    banner "Phase 5 — Disabling first-boot service"

    systemctl disable first-boot.service || warn "failed to disable first-boot.service"
    ok "first-boot service disabled"
}

phase_51_summary() {
    banner "Provisioning complete — summary"

    echo ""
    echo "  Duration: $(elapsed)"
    echo "  Warnings: ${#WARNINGS[@]}"
    echo "  Skipped:  ${#SKIPPED[@]}"
    echo ""

    if [[ ${#WARNINGS[@]} -gt 0 ]]; then
        echo "  Warnings:"
        for w in "${WARNINGS[@]}"; do
            echo "    - ${w}"
        done
        echo ""
    fi

    echo "  Your system is ready. You may need to:"
    echo "    - Log out and back in for the docker group to take effect"
    echo "    - Restart to apply all GNOME settings"
    echo ""
    echo "  Tip: run 'provision' again anytime to re-run this script safely."
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

main() {
    # Must run as root (first-boot.service runs as root).
    if [[ "${EUID}" -ne 0 ]]; then
        echo "ERROR: provision.sh must run as root (use sudo)." >&2
        exit 1
    fi

    echo ""
    echo "================================================================"
    echo "  provisioner-ubuntu — post-install setup"
    echo "  started at $(date '+%Y-%m-%d %H:%M:%S')"
    echo "================================================================"

    phase_00_apt_update
    phase_01_core_packages
    phase_02_docker
    phase_03_gpu_drivers
    phase_05_cc_switch
    phase_07_clash_verge

    phase_10_homebrew
    phase_11_mise
    phase_12_brew_packages
    phase_13_mise_tools

    phase_20_npm_globals
    phase_21_opencode
    phase_22_claude_code

    phase_30_gnome_theme
    phase_31_gnome_dock
    phase_32_gnome_shortcuts
    phase_33_fcitx5_chinese

    phase_40_shell_env
    phase_41_git_config
    phase_42_dotfiles
    phase_43_mount_data_disks

    phase_50_disable_service

    # Self-test: prove a non-interactive shell (the way an Agent / SSH
    # `bash -c` runs) can actually find the mise/brew tools provisioned
    # above. If this fails, the Agent will hit "command not found" later.
    if [[ -x /usr/local/bin/test-env-loading ]]; then
        echo ""
        /usr/local/bin/test-env-loading || warn "env-loading self-test reported gaps"
    fi

    phase_51_summary
}

main "$@"
