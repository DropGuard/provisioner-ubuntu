#!/usr/bin/env bash
#
# 配置 fcitx5 拼音输入法（Ubuntu + GNOME + Wayland）。
# 两种调用方式：
#   - 无参数（first-boot 时调用）：安装 fcitx5、写 IM 环境变量、写自启动项。
#     first-boot 跑在 Before=systemd-user-sessions，session D-Bus 未就绪，
#     所以"加拼音输入法组"这一需要 D-Bus 的步骤不在这里做。
#   - "pinyin" 参数（登录后 autostart 调用）：等 D-Bus 就绪后把拼音加入
#     默认输入法组并设为当前输入法。
#
set -euo pipefail
log() { printf '\033[32m[OK]\033[0m   %s\n' "$*"; }

if [ "${1:-}" = "pinyin" ]; then
  # ── 登录后：D-Bus 已就绪，加拼音输入法组 ─────────────────────────
  for _ in $(seq 1 30); do
    gdbus call --session --dest org.fcitx.Fcitx5 --object-path /controller \
      --method org.fcitx.Fcitx.Controller1.State >/dev/null 2>&1 && break
    sleep 1
  done
  if gdbus call --session --dest org.fcitx.Fcitx5 --object-path /controller \
       --method org.fcitx.Fcitx.Controller1.State >/dev/null 2>&1; then
    gdbus call --session --dest org.fcitx.Fcitx5 --object-path /controller \
      --method org.fcitx.Fcitx.Controller1.SetInputMethodGroupInfo \
      "Default" "pinyin" "[('keyboard-us',''),('pinyin','')]" >/dev/null 2>&1 \
      && gdbus call --session --dest org.fcitx.Fcitx5 --object-path /controller \
           --method org.fcitx.Fcitx.Controller1.Save >/dev/null 2>&1
    gdbus call --session --dest org.fcitx.Fcitx5 --object-path /controller \
      --method org.fcitx.Fcitx.Controller1.SetCurrentIM "pinyin" >/dev/null 2>&1 || true
    log "已把拼音 + 英文键盘加入默认输入法组并切到拼音。"
  else
    log "fcitx5 未响应（尚未启动？），拼音组请用 fcitx5-configtool 手动添加。"
  fi
  exit 0
fi

# ── first-boot：安装 + 环境变量 + 自启动 ───────────────────────────────

# 1. 安装（缺才装，避免每次都要 sudo）
if ! command -v fcitx5 >/dev/null; then
  log "安装 fcitx5 及拼音引擎…"
  sudo apt-get install -y fcitx5 fcitx5-config-qt fcitx5-chinese-addons \
    fcitx5-frontend-gtk3 fcitx5-frontend-gtk2 fcitx5-frontend-gtk4 \
    fcitx5-frontend-qt5 fcitx5-frontend-qt6
else
  log "fcitx5 已安装，跳过。"
fi

# 2. 环境变量——Wayland 不走 ~/.xinputrc，必须靠 environment.d
mkdir -p ~/.config/environment.d
cat > ~/.config/environment.d/fcitx5.conf <<'EOF'
GTK_IM_MODULE=fcitx5
QT_IM_MODULE=fcitx5
XMODIFIERS=@im=fcitx5
SDL_IM_MODULE=fcitx5
GLFW_IM_MODULE=ibus
EOF
log "已写入 ~/.config/environment.d/fcitx5.conf"

# 3. 自启动：fcitx5 本体 + 拼音组设置（登录后执行，避开 first-boot 的 D-Bus 时序）
mkdir -p ~/.config/autostart
cat > ~/.config/autostart/fcitx5.desktop <<'EOF'
[Desktop Entry]
Type=Application
Name=Fcitx 5
Exec=/usr/bin/fcitx5
Terminal=false
X-GNOME-Autostart-enabled=true
X-GNOME-AutoRestart=true
EOF
cat > ~/.config/autostart/fcitx5-pinyin.desktop <<'EOF'
[Desktop Entry]
Type=Application
Name=Fcitx5 Pinyin Setup
Exec=/usr/local/bin/setup-fcitx5-chinese.sh pinyin
Terminal=false
X-GNOME-Autostart-enabled=true
X-GNOME-Autostart-Delay=5
EOF
log "已写入 fcitx5 自启动 + 拼音组自启动（登录后生效，新登录可用 Ctrl+Space 切换）。"
