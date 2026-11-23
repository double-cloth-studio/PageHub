#!/usr/bin/env bash
set -Eeuo pipefail
source "$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/scripts/lib/common.sh"
require_root
config_file=''
apply_options=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    -c|--config) [[ $# -ge 2 ]] || die '需要配置文件路径'; config_file="$(realpath -- "$2")"; shift 2 ;;
    --approved-plan) [[ $# -ge 2 ]] || die '需要计划摘要'; apply_options+=(--approved-plan "$2"); shift 2 ;;
    --adopt) apply_options+=(--adopt); shift ;;
    *) die '用法: install.sh [-c pagehub.yaml] [--approved-plan <摘要>] [--adopt]' ;;
  esac
done
if [[ -f "$PAGEHUB_SOURCE/offline.env" ]]; then
  set -a
  source "$PAGEHUB_SOURCE/offline.env"
  set +a
fi
if [[ -n "$config_file" ]]; then
  source "$PAGEHUB_SOURCE/scripts/bootstrap-config.sh"
  [[ ! -L /etc/pagehub && ! -L /etc/pagehub/pagehub.yaml ]] || die '声明式配置目录和文件须为普通路径'
  install -d -m 0700 /etc/pagehub
  if [[ "$config_file" != /etc/pagehub/pagehub.yaml ]]; then install -m 0600 "$config_file" /etc/pagehub/pagehub.yaml; fi
  chown root:root /etc/pagehub /etc/pagehub/pagehub.yaml
  chmod 0600 /etc/pagehub/pagehub.yaml
fi
platform_lock
if [[ -f "$PAGEHUB_ROOT/state/install-state.json" ]]; then
  log '检测到已安装平台，保留配置与凭据并执行恢复'
  if [[ -n "$config_file" ]]; then
    export PAGEHUB_ADMIN_PORT=8091
    set -a
    source "$PAGEHUB_ROOT/config/pagehub.env"
    set +a
    runner desired validate -c "$config_file"
    if ! grep -q '^PAGEHUB_CONFIG_FILE=' "$PAGEHUB_ROOT/config/pagehub.env"; then printf 'PAGEHUB_CONFIG_FILE=/etc/pagehub/pagehub.yaml\n' >> "$PAGEHUB_ROOT/config/pagehub.env"; fi
    export PAGEHUB_CONFIG_FILE=/etc/pagehub/pagehub.yaml
  fi
  bash "$PAGEHUB_ROOT/current/scripts/start-services.sh"
  flock -u 9
  if [[ -n "$config_file" ]]; then runner desired apply -c "$config_file" "${apply_options[@]}"; fi
  exit 0
fi
bash "$PAGEHUB_SOURCE/scripts/preflight.sh"
bash "$PAGEHUB_SOURCE/scripts/configure.sh"
source "$PAGEHUB_ROOT/config/pagehub.env"
bash "$PAGEHUB_SOURCE/scripts/build.sh"
bash "$PAGEHUB_SOURCE/scripts/deploy.sh"
bash "$PAGEHUB_ROOT/current/scripts/enable-autostart.sh"
bash "$PAGEHUB_ROOT/current/scripts/start-services.sh"
bash "$PAGEHUB_ROOT/current/scripts/import-bundled-apps.sh"
bash "$PAGEHUB_ROOT/current/scripts/healthcheck.sh"
date -u +%FT%TZ > "$PAGEHUB_ROOT/current/.pagehub-verified"
printf '{"version":"%s","installed_at":"%s","root":"%s"}\n' "$(cat "$PAGEHUB_ROOT/current/VERSION")" "$(date -u +%FT%TZ)" "$PAGEHUB_ROOT" > "$PAGEHUB_ROOT/state/install-state.json.pending"
mv -f "$PAGEHUB_ROOT/state/install-state.json.pending" "$PAGEHUB_ROOT/state/install-state.json"
rm -f -- "$PAGEHUB_ROOT/state/uninstalled.json"
flock -u 9
systemctl start pagehub.service
if [[ -n "$config_file" ]]; then runner desired apply -c "$config_file" "${apply_options[@]}"; fi
log '安装完成，管理员 Token 保存在 secrets/admin.token，请通过 SSH 转发 8091 端口访问 /admin/'
