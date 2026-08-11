#!/usr/bin/env bash
set -euo pipefail

log() { printf '\033[32m[OK]\033[0m   %s\n' "$*"; }

log "等待网络就绪..."
until ping -c 1 -W 1 1.1.1.1 >/dev/null 2>&1; do
  sleep 1
done

log "网络已就绪。先替换软件源为阿里云..."
sed -i 's/cn\.archive\.ubuntu\.com/mirrors.aliyun.com/g' /etc/apt/sources.list.d/ubuntu.sources
sed -i 's/archive\.ubuntu\.com/mirrors.aliyun.com/g' /etc/apt/sources.list.d/ubuntu.sources
sed -i 's/security\.ubuntu\.com/mirrors.aliyun.com/g' /etc/apt/sources.list.d/ubuntu.sources

log "开始更新包列表并安装 Ansible..."
export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y ansible git

log "Ansible 安装成功。开始执行自动化剧本..."
# playbook 的路径在 cloud-init late-commands 中指定，固定复制到 /usr/local/share/provisioner-ubuntu/ansible
# profile_tasks 回调打印每个任务的耗时，便于定位慢/卡任务(否则 ansible stdout 在任务结束后才 flush)。
export ANSIBLE_CALLBACKS_ENABLED=profile_tasks
ansible-playbook -c local /usr/local/share/provisioner-ubuntu/ansible/main.yml

log "装机流水线执行完毕！"
