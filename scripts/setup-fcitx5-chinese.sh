#!/usr/bin/env bash
#
# 在本机 (Ubuntu + GNOME + Wayland) 配置 fcitx5 拼音输入法。
# 设计为可重复执行：已装则跳过安装，已配置则覆盖为正确状态。
#
set -euo pipefail
log() { printf '\033[32m[OK]\033[0m   %s\n' "$*"; }

# 1. 安装（缺才装，避免每次都要 sudo）
if ! command -v fcitx5 >/dev/null; then
  log "安装 fcitx5 及拼音引擎…"
  sudo apt-get install -y fcitx5 fcitx5-config-gui fcitx5-chinese-addons \
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

# 3. 自启动兜底（防止下次登录 fcitx5 不自动起）
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
log "已写入 ~/.config/autostart/fcitx5.desktop"

# 4. 启动 fcitx5 并加好拼音输入法
export GTK_IM_MODULE=fcitx5 QT_IM_MODULE=fcitx5 XMODIFIERS=@im=fcitx5 SDL_IM_MODULE=fcitx5

if pgrep -u "$USER" -x fcitx5 >/dev/null; then
  fcitx5-remote -e >/dev/null 2>&1 || true
  for _ in $(seq 1 20); do pgrep -u "$USER" -x fcitx5 >/dev/null || break; sleep 0.3; done
fi
rm -f /tmp/fcitx5.log
nohup fcitx5 >/tmp/fcitx5.log 2>&1 &
log "已启动 fcitx5 (PID: $!)"

# 等 D-Bus 真正可达
for _ in $(seq 1 30); do
  busctl --user call org.fcitx.Fcitx5 /controller org.fcitx.Fcitx.Controller1 State >/dev/null 2>&1 && break
  sleep 0.5
done

if busctl --user call org.fcitx.Fcitx5 /controller org.fcitx.Fcitx.Controller1 State >/dev/null 2>&1; then
  gdbus call --session --dest org.fcitx.Fcitx5 --object-path /controller \
    --method org.fcitx.Fcitx.Controller1.SetInputMethodGroupInfo \
    "Default" "pinyin" "[( 'keyboard-us', '' ), ( 'pinyin', '' )]" >/dev/null 2>&1 \
    && gdbus call --session --dest org.fcitx.Fcitx5 --object-path /controller \
         --method org.fcitx.Fcitx.Controller1.Save >/dev/null 2>&1
  gdbus call --session --dest org.fcitx.Fcitx5 --object-path /controller \
    --method org.fcitx.Fcitx.Controller1.SetCurrentIM "'pinyin'" >/dev/null 2>&1 || true
  log "已把拼音 + 英文键盘加入默认输入法组并切到拼音。"
else
  log "D-Bus 未就绪，请稍后手动用 fcitx5-configtool 加 Pinyin。"
fi

# 5. 验证（无 GUI 焦点时 CurrentInputMethod 会返回空，故以组内含 pinyin 为准）
if gdbus call --session --dest org.fcitx.Fcitx5 --object-path /controller \
     --method org.fcitx.Fcitx.Controller1.InputMethodGroupInfo "'Default'" 2>/dev/null \
     | grep -q "'pinyin'"; then
  log "验证通过：默认输入法组已包含拼音。"
else
  log "验证未通过：组里未找到 pinyin，请手动检查 fcitx5-configtool。"
fi
log "完成。本会话可直接用 Ctrl+Space 切换；新登录建议注销重登一次使环境变量生效。"
