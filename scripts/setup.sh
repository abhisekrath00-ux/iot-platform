#!/usr/bin/env bash
# One entry point for both install shapes. Nothing here replaces install.sh: option 1 hands over to it unchanged.
#
#   bash scripts/setup.sh                   menu: 1) single node  2) multi node (3-node HA)
#   bash scripts/setup.sh single [args]     same as: bash scripts/install.sh [args]
#   bash scripts/setup.sh multi  [args]     same as: bash scripts/install-ha.sh [args] (plan|render|check|drill)
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
mode="${1:-}"; [ $# -gt 0 ] && shift || true
if [ -z "$mode" ]; then
  if [ ! -t 0 ]; then echo "usage: setup.sh single|multi [args]" >&2; exit 2; fi
  echo "HexThings setup"
  echo "  1) Single node  - one machine, Docker Compose (the existing guided installer)"
  echo "  2) Multi node   - 3 machines with automatic database failover (generator; see docs/ha-multi-node.md)"
  read -r -p "Choose 1 or 2: " c
  case "$c" in 1) mode=single ;; 2) mode=multi ;; *) echo "no choice made" >&2; exit 2 ;; esac
fi
case "$mode" in
  single) exec bash "$here/install.sh" "$@" ;;
  multi)  [ $# -gt 0 ] || { echo "multi node: bash scripts/setup.sh multi plan|render|check|drill --nodes n1=IP,n2=IP,n3=IP --vip IP"; echo "details: docs/ha-multi-node.md"; exit 0; }
          exec bash "$here/install-ha.sh" "$@" ;;
  -h|--help) sed -n 2,7p "$0" ;;
  *) echo "usage: setup.sh single|multi [args]" >&2; exit 2 ;;
esac
