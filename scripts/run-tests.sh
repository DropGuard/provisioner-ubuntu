#!/usr/bin/env bash
set -eou pipefail

# 默认使用 Server 镜像，允许通过环境变量覆盖
ISO_PATH="${ISO_PATH:-/mnt/ssd1t/ubuntu-26.04-live-server-amd64.iso}"
CACHE_DIR="${XDG_CACHE_HOME:-/mnt/ssd1t/cache}"
WORK_DIR="${WORK_DIR:-/mnt/ssd1t/vmtest-work}"
HOST_PROXY_TEST="http://127.0.0.1:3142"
VM_APT_PROXY="http://10.0.2.2:3142"

echo "=================================================="
echo "🚀 自动化端到端测试流水线 (E2E Test Pipeline)"
echo "ISO 镜像: $ISO_PATH"
echo "缓存目录: $CACHE_DIR"
echo "=================================================="

# 切换到 go 框架目录
cd "$(dirname "$0")/../go"

if [ ! -f "$ISO_PATH" ]; then
    echo "❌ 错误: 找不到 ISO 镜像文件 $ISO_PATH"
    exit 1
fi

echo -e "\n[1/2] 正在烘焙黄金镜像 (Phase A) ..."
# 根据代理是否响应来决定是否添加代理参数
PROXY_ARGS=()
if curl -s -m 1 "$HOST_PROXY_TEST" >/dev/null; then
    echo "  > 探测到本地代理 $HOST_PROXY_TEST，为虚拟机启用包缓存 ($VM_APT_PROXY)"
    PROXY_ARGS=("--apt-proxy" "$VM_APT_PROXY")
else
    echo "  > 未探测到本地缓存代理，将直连下载"
fi

XDG_CACHE_HOME="$CACHE_DIR" go run ./cmd/provisioner test-vm \
    --golden \
    --iso "$ISO_PATH" \
    --work "$WORK_DIR" \
    "${PROXY_ARGS[@]}"

echo -e "\n[2/2] 正在执行端到端断言测试 (Phase B) ..."
XDG_CACHE_HOME="$CACHE_DIR" go run ./cmd/provisioner test-e2e \
    --iso "$ISO_PATH" \
    --serial "50026B727200FDDC"

echo -e "\n[3/3] 正在执行全量 Provision 并验证 (Phase C) ..."
# shellcheck disable=SC2012
GOLDEN_BASE=$(ls -t "$CACHE_DIR"/vmtest-golden/*.qcow2 2>/dev/null | head -n 1)
if [ -z "$GOLDEN_BASE" ]; then
    echo "❌ 错误: 找不到生成的黄金镜像"
    exit 1
fi

XDG_CACHE_HOME="$CACHE_DIR" go run ./cmd/provisioner test-provision \
    --base "$GOLDEN_BASE" \
    --repo ".." \
    --check "docker --version && eza --version && rg --version && echo Phase C Pass!"

echo -e "\n✅ 所有测试流程执行完毕！"
