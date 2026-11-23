#!/usr/bin/env bash
set -Eeuo pipefail
source "$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/scripts/lib/common.sh"
require_root

case "${1:-}" in
  ''|--yes|--dry-run) [[ $# -le 1 ]] || die '用法: uninstall.sh [--dry-run|--yes]' ;;
  *) die '用法: uninstall.sh [--dry-run|--yes]' ;;
esac

state="$PAGEHUB_ROOT/state/install-state.json"
if [[ ! -f "$state" && -f "$PAGEHUB_ROOT/state/uninstalled.json" ]]; then
  log 'PageHub 已卸载'
  exit 0
fi
require_install
[[ -f "$PAGEHUB_ROOT/current/runtime.env" ]] || die '当前发行版缺失，请先恢复安装'

log "卸载 $PAGEHUB_ROOT 的 PageHub 服务、网关和门户容器"
log '保留已接入应用及其容器、卷、配置、备份、历史版本和 Docker 网络'
if [[ "${1:-}" != --dry-run ]]; then
  if [[ "${1:-}" != --yes ]]; then
    [[ -t 0 ]] || die '非交互卸载需要 --yes'
    read -r -p '输入 PageHub 确认卸载: ' answer
    [[ "$answer" == PageHub ]] || die '未确认卸载'
  fi
  platform_lock
  require_install
fi

units=()
enabled_units=()
unit_files=()
for name in pagehub pagehub-updater pagehub-manager pagehub-runner pagehub-social; do
  unit="$name.service"
  file="/etc/systemd/system/$unit"
  if [[ -e "$file" || -L "$file" ]]; then
    grep -Fxq "EnvironmentFile=$PAGEHUB_ROOT/config/pagehub.env" "$file" ||
      die "$unit 不属于当前安装目录"
    units+=("$unit")
    unit_files+=("$file")
    if [[ "$name" != pagehub-updater ]]; then enabled_units+=("$unit"); fi
  fi
done

launcher=/usr/local/bin/pagehub
if [[ -e "$launcher" || -L "$launcher" ]]; then
  grep -Fxq "exec bash $PAGEHUB_ROOT/current/pagehub.sh \"\$@\"" "$launcher" ||
    die "$launcher 不属于当前安装目录"
fi

if [[ "${1:-}" == --dry-run ]]; then exit 0; fi
docker info >/dev/null
if (( ${#units[@]} )); then systemctl stop "${units[@]}"; fi
if (( ${#enabled_units[@]} )); then systemctl disable "${enabled_units[@]}"; fi
platform_compose gateway down
platform_compose portal down
rm -f -- "$launcher" "${unit_files[@]}"
systemctl daemon-reload
mv -fT -- "$state" "$PAGEHUB_ROOT/state/uninstalled.json"
log 'PageHub 已卸载，重新运行 install.sh 可恢复平台'
