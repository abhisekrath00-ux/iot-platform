#!/usr/bin/env bash
# get.sh against stubbed prereq/install/sg: exit codes from prereq (0, 3 with and without a working `sg docker`, 1).
ROOT=$(cd "$(dirname "$0")/.." && pwd); fails=0
check() { if eval "$2"; then echo "ok:   $1"; else echo "FAIL: $1"; fails=$((fails+1)); fi; }
mk() { # $1 = prereq exit code, $2 = sg works (1/0)
  T=$(mktemp -d); mkdir -p "$T/w/hx/.git" "$T/w/hx/scripts" "$T/bin" "$T/home"
  cp "$ROOT/scripts/get.sh" "$T/w/get.sh"
  printf '#!/bin/sh\nexit %s\n' "$1" > "$T/w/hx/scripts/prereq.sh"
  printf '#!/bin/sh\necho "INSTALL-RAN $*"\n' > "$T/w/hx/scripts/install.sh"
  printf '#!/bin/sh\nexit 0\n' > "$T/w/hx/scripts/hexthings"
  printf '#!/bin/sh\nshift; [ "$1" = -c ] || exit 9\ncase "$2" in "docker info") [ %s = 1 ] || exit 1; exit 0;; *) echo "SG-RAN $2"; exit 0;; esac\n' "$2" > "$T/bin/sg"
  chmod +x "$T/bin/sg" "$T/w/hx/scripts/"*
}
run() { (cd "$T/w" && HOME="$T/home" PATH="$T/bin:$PATH" HEXTHINGS_DIR=hx bash get.sh "$@" > "$T/out" 2>&1 < /dev/null); rc=$?; }
mk 0 0; run;        check "prereq 0: installs normally" '[ $rc -eq 0 ] && grep -q INSTALL-RAN "$T/out"'
mk 3 1; run;        check "prereq 3, sg works: runs the installer inside the docker group" '[ $rc -eq 0 ] && grep -q "SG-RAN bash scripts/install.sh" "$T/out" && ! grep -q INSTALL-RAN "$T/out"'
mk 3 0; run;        check "prereq 3, sg fails: tells the user to log out/in or newgrp, exit 3" '[ $rc -eq 3 ] && grep -q "newgrp docker" "$T/out" && ! grep -q INSTALL-RAN "$T/out"'
mk 1 1; run;        check "prereq 1: stops with exit 1" '[ $rc -eq 1 ] && ! grep -q INSTALL-RAN "$T/out"'
mk 2 1; run;        check "prereq 2: stops with exit 2" '[ $rc -eq 2 ] && ! grep -q INSTALL-RAN "$T/out"'
echo; [ $fails -eq 0 ] && echo "all get.sh checks passed" || { echo "$fails failed"; exit 1; }
