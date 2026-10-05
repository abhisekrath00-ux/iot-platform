# Shared HexThings terminal look: big gradient wordmark, animated reveal, footer, spinner, menu helper.
# Dot-sourced by hexthings.ps1 and install.ps1. Works on Windows PowerShell 5.1 and PowerShell 7.
# Fallbacks: no colour when NO_COLOR is set or output is redirected; ASCII blocks and "<3" when the console
# is not UTF-8 (the legacy cp437/cp850 consoles can draw the block, but not the heart).
$global:HxColor = (-not $env:NO_COLOR) -and (-not [Console]::IsOutputRedirected)
$global:HxEnc = ""; try { $global:HxEnc = [Console]::OutputEncoding.WebName } catch { }
$global:HxUtf = ($global:HxEnc -match "utf") -and (-not $env:HEXTHINGS_ASCII)
$global:HxBlock = if (((($global:HxEnc -match "utf") -or ($global:HxEnc -match "ibm437") -or ($global:HxEnc -match "ibm850"))) -and (-not $env:HEXTHINGS_ASCII)) { [string][char]0x2588 } else { "#" }
$global:HxHeart = if ($global:HxUtf) { [string][char]0x2764 } else { "<3" }
$global:HxTagline = "Industrial IoT platform"

# 7-row font for the letters in "HexThings". '#' is a filled cell.
$global:HxFont = @{
  "Hcap" = @("#...#", "#...#", "#...#", "#####", "#...#", "#...#", ".....")
  "e"    = @(".....", ".###.", "#...#", "#####", "#....", ".####", ".....")
  "x"    = @(".....", "#...#", ".#.#.", "..#..", ".#.#.", "#...#", ".....")
  "Tcap" = @("#####", "..#..", "..#..", "..#..", "..#..", "..#..", ".....")
  "hlow" = @("#....", "#....", "#.##.", "##..#", "#...#", "#...#", ".....")
  "i"    = @(".#.", "...", "##.", ".#.", ".#.", "###", "...")
  "n"    = @(".....", ".....", "#.##.", "##..#", "#...#", "#...#", ".....")
  "g"    = @(".....", ".####", "#...#", "#...#", ".####", "....#", ".###.")
  "s"    = @(".....", ".####", "#....", ".###.", "....#", "####.", ".....")
}
# One colour per letter, left to right: a cyan -> blue -> magenta sweep using only the 16 console colours,
# so it looks the same in Windows PowerShell 5.1, Windows Terminal and Linux terminals.
$global:HxGradient = @("Cyan", "Cyan", "Cyan", "Blue", "Blue", "Blue", "Magenta", "Magenta", "Magenta")

function Hx-Height { try { return [Console]::WindowHeight } catch { return 40 } }
function Hx-Width { try { return [Console]::WindowWidth } catch { return 80 } }

# Returns the 7 text rows of the wordmark and, per row, the (text, colour) segments.
function Hx-WordmarkRows {
  $word = @("Hcap", "e", "x", "Tcap", "hlow", "i", "n", "g", "s")
  $rows = @()
  for ($r = 0; $r -lt 7; $r++) {
    $segs = @()
    for ($k = 0; $k -lt $word.Count; $k++) {
      $g = $global:HxFont[$word[$k]][$r]
      $txt = ($g.ToCharArray() | ForEach-Object { if ($_ -eq "#") { $global:HxBlock } else { " " } }) -join ""
      $segs += , @(($txt + " "), $global:HxGradient[$k])
    }
    $rows += , $segs
  }
  return , $rows
}

function Hx-Wordmark([string]$Sub = "", [switch]$Animate, [switch]$Compact) {
  Write-Host ""
  $width = Hx-Width
  if ($width -lt 56 -or $Compact) {  # narrow or short console: one line
    if ($global:HxColor) { Write-Host "  HexThings" -ForegroundColor Cyan -NoNewline; if ($Sub) { Write-Host "  $Sub" -ForegroundColor DarkGray } else { Write-Host "" } } else { Write-Host "  HexThings  $Sub" }
    return
  }
  $rows = Hx-WordmarkRows
  $animate = $Animate -and $global:HxColor -and (-not $env:HEXTHINGS_NO_ANIM)
  foreach ($row in $rows) {
    Write-Host "  " -NoNewline
    foreach ($seg in $row) {
      if ($global:HxColor) { Write-Host $seg[0] -ForegroundColor $seg[1] -NoNewline } else { Write-Host $seg[0] -NoNewline }
      if ($animate) { Start-Sleep -Milliseconds 18 }
    }
    Write-Host ""
  }
  Write-Host ""
  $line = "  " + $global:HxTagline + $(if ($Sub) { "  " + $global:HxBlock + "  " + $Sub } else { "" })
  if ($global:HxColor) { Write-Host $line -ForegroundColor DarkGray } else { Write-Host $line }
}

function Hx-Footer {
  if ($global:HxColor) {
    Write-Host "  Made with " -ForegroundColor DarkGray -NoNewline
    Write-Host $global:HxHeart -ForegroundColor Red -NoNewline
    Write-Host " by Hexmon Technology" -ForegroundColor DarkGray
  } else { Write-Host "  Made with $($global:HxHeart) by Hexmon Technology" }
}

# Startup: wordmark with a reveal, then a short progress sweep. Call once at the top of an interactive run.
function Hx-Startup([string]$Sub = "") {
  Hx-Wordmark $Sub -Animate -Compact:((Hx-Height) -lt 36)
  Hx-Footer
  Write-Host ""
}

# Pick one entry from a list. Arrow keys + Enter, or the number, or q. Returns the index, or -1 for quit.
# Without an interactive console it returns -2 so the caller can print plain help instead.
function Hx-Menu([string]$Title, [string[]]$Items, [string[]]$Hints) {
  $interactive = $global:HxColor -and (-not [Console]::IsInputRedirected)
  if (-not $interactive) { return -2 }
  $sel = 0
  $arrow = if ($global:HxUtf) { [string][char]0x276F } else { ">" }
  [Console]::CursorVisible = $false
  $top = [Console]::CursorTop
  try {
    while ($true) {
      [Console]::SetCursorPosition(0, $top)
      Write-Host ("  " + $Title) -ForegroundColor Cyan
      Write-Host ""
      for ($i = 0; $i -lt $Items.Count; $i++) {
        $num = if ($i -lt 9) { [string]($i + 1) } else { " " }
        $pad = "  {0} {1} {2}" -f $(if ($i -eq $sel) { $arrow } else { " " }), $num, $Items[$i].PadRight(26)
        if ($i -eq $sel) { Write-Host $pad -ForegroundColor Cyan -NoNewline; Write-Host (($Hints[$i] + "").PadRight(48)) -ForegroundColor White }
        else { Write-Host $pad -ForegroundColor Gray -NoNewline; Write-Host (($Hints[$i] + "").PadRight(48)) -ForegroundColor DarkGray }
      }
      Write-Host ""
      Write-Host "  arrows move   enter select   1-9 jump   q quit" -ForegroundColor DarkGray
      $top = [Math]::Max(0, [Console]::CursorTop - ($Items.Count + 4))  # stays right even if the window scrolled
      $k = [Console]::ReadKey($true)
      switch ($k.Key) {
        "UpArrow" { $sel = ($sel + $Items.Count - 1) % $Items.Count }
        "DownArrow" { $sel = ($sel + 1) % $Items.Count }
        "Home" { $sel = 0 }
        "End" { $sel = $Items.Count - 1 }
        "Enter" { return $sel }
        "Q" { return -1 }
        "Escape" { return -1 }
        default { $c = [string]$k.KeyChar; if ($c -match "^[1-9]$" -and ([int]$c - 1) -lt $Items.Count) { return ([int]$c - 1) } }
      }
    }
  } finally { [Console]::CursorVisible = $true }
}
