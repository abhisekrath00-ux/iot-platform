# Shared HexThings terminal look for scripts/setup.sh: gradient wordmark with row-by-row reveal, footer.
# Copied unchanged from scripts/install.sh (which keeps its own copy); scripts/test-install-ha.sh fails if they drift.
# ---- look and feel ----------------------------------------------------------------------------
# Colour only on a real terminal (and not when NO_COLOR is set); Unicode only when the locale says
# UTF-8. Everything else gets plain ASCII with the same words, so logs and CI stay readable.
COLOR=0; UNI=0
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ] && [ "${TERM:-dumb}" != dumb ]; then COLOR=1; fi
case "${LC_ALL:-${LC_CTYPE:-${LANG:-}}}" in *[Uu][Tt][Ff]-8*|*[Uu][Tt][Ff]8*) UNI=1 ;; esac
[ "${HEXTHINGS_ASCII:-}" = 1 ] && UNI=0
if [ "$COLOR" = 1 ]; then
  B=$'\033[1m'; D=$'\033[2m'; N=$'\033[0m'; G=$'\033[32m'; Y=$'\033[33m'; R=$'\033[31m'
  C1=$'\033[38;5;45m'; C2=$'\033[38;5;39m'; C3=$'\033[38;5;33m'; C4=$'\033[38;5;63m'; C5=$'\033[38;5;99m'
else B=; D=; N=; G=; Y=; R=; C1=; C2=; C3=; C4=; C5=; fi
if [ "$UNI" = 1 ]; then I_OK="✔"; I_WARN="▲"; I_FAIL="✖"; BAR_ON="█"; BAR_OFF="░"; SPIN=(⠋ ⠙ ⠹ ⠸ ⠼ ⠴ ⠦ ⠧ ⠇ ⠏)
else I_OK="ok"; I_WARN="warn"; I_FAIL="FAIL"; BAR_ON="#"; BAR_OFF="-"; SPIN=('|' '/' '-' '\'); fi
plain() { printf '%s\n' "$(printf '%s' "$*" | sed $'s/\033\\[[0-9;]*m//g')" >> "$LOG"; }
# glyph/wordmark/footer from install.sh
# glyph LETTER ROW -> 7-row font for "HexThings" ('#' is a filled cell). Plain case, so it works in bash 3.2 (macOS).
glyph() {
  case "$1$2" in
    H0|H1|H2) echo "#...#";; H3) echo "#####";; H4|H5) echo "#...#";; H6) echo ".....";;
    e0) echo ".....";; e1) echo ".###.";; e2) echo "#...#";; e3) echo "#####";; e4) echo "#....";; e5) echo ".####";; e6) echo ".....";;
    x0) echo ".....";; x1) echo "#...#";; x2) echo ".#.#.";; x3) echo "..#..";; x4) echo ".#.#.";; x5) echo "#...#";; x6) echo ".....";;
    T0) echo "#####";; T1|T2|T3|T4|T5) echo "..#..";; T6) echo ".....";;
    h0|h1) echo "#....";; h2) echo "#.##.";; h3) echo "##..#";; h4|h5) echo "#...#";; h6) echo ".....";;
    i0) echo ".#.";; i1) echo "...";; i2) echo "##.";; i3|i4) echo ".#.";; i5) echo "###";; i6) echo "...";;
    n0|n1) echo ".....";; n2) echo "#.##.";; n3) echo "##..#";; n4|n5) echo "#...#";; n6) echo ".....";;
    g0) echo ".....";; g1) echo ".####";; g2|g3) echo "#...#";; g4) echo ".####";; g5) echo "....#";; g6) echo ".###.";;
    s0) echo ".....";; s1) echo ".####";; s2) echo "#....";; s3) echo ".###.";; s4) echo "....#";; s5) echo "####.";; s6) echo ".....";;
  esac
}
# wordmark: big gradient "HexThings" with a short row-by-row reveal. Plain "HexThings" when there is no colour or the window is narrow.
wordmark() { # wordmark "subtitle"
  local sub=${1:-} cols=${COLUMNS:-$(tput cols 2>/dev/null || echo 80)}
  if [ "$COLOR" = 0 ] || [ "$cols" -lt 56 ]; then printf '%s\n' "" "  HexThings   $sub" ""; return; fi
  local letters="HexThings" blk="#" r k ch g line i c pal
  [ "$UNI" = 1 ] && blk="█"
  if [ "$(tput colors 2>/dev/null || echo 8)" -ge 256 ]; then pal=(51 51 45 39 33 63 99 135 171)
  else pal=(36 36 36 34 34 34 35 35 35); fi
  echo ""
  for r in 0 1 2 3 4 5 6; do
    printf '  '
    for ((k=0;k<${#letters};k++)); do
      ch=${letters:$k:1}; g=$(glyph "$ch" "$r"); line=""
      for ((i=0;i<${#g};i++)); do if [ "${g:$i:1}" = "#" ]; then line+="$blk"; else line+=" "; fi; done
      if [ "$(tput colors 2>/dev/null || echo 8)" -ge 256 ]; then printf '\033[38;5;%sm%s \033[0m' "${pal[$k]}" "$line"
      else printf '\033[%sm%s \033[0m' "${pal[$k]}" "$line"; fi
      [ -t 1 ] && sleep 0.012
    done
    echo ""
  done
  echo ""; printf '  %sIndustrial IoT platform%s%s%s\n' "$D" "$([ -n "$sub" ] && printf '  %s  %s' "$blk" "$sub")" "" "$N"
}
footer() {
  local heart="<3"; [ "$UNI" = 1 ] && heart="❤"
  if [ "$COLOR" = 1 ]; then printf '  %sMade with %s\033[31m%s%s %sby Hexmon Technology%s\n' "$D" "$N" "$heart" "$N" "$D" "$N"; else printf '  Made with %s by Hexmon Technology\n' "$heart"; fi
}
