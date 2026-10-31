# Read-only archive validation, then extraction into a fresh staging directory.
# This is a bounded format/path guard, not signature verification or a sandbox.
function Prepare-PatchSource([string]$ZipPath, [string]$Destination) {
  $ErrorActionPreference = 'Stop'
  Add-Type -AssemblyName System.IO.Compression.FileSystem
  $stage = Join-Path ([IO.Path]::GetTempPath()) ('hexthings-patch-' + [Guid]::NewGuid().ToString('N'))
  [void][IO.Directory]::CreateDirectory($stage)
  $archive = $null
  try {
    $snapshot = Join-Path $stage 'source.zip'
    if (-not (Test-Path -LiteralPath $ZipPath -PathType Leaf)) { throw 'Patch ZIP is missing' }
    if ((Get-Item -LiteralPath $ZipPath).Length -gt 268435456) { throw 'Patch ZIP exceeds 256 MiB' }
    Copy-Item -LiteralPath $ZipPath -Destination $snapshot
    $digest = (Get-FileHash -LiteralPath $snapshot -Algorithm SHA256).Hash.ToLower()
    $archive = [IO.Compression.ZipFile]::OpenRead($snapshot)
    if ($archive.Entries.Count -gt 10000) { throw 'Too many archive entries' }
    $seen = @{}; $files = @(); $rootName = ''; [long]$total = 0
    foreach ($entry in $archive.Entries) {
      $name = $entry.FullName
      if (-not $name -or $name.Contains('\') -or $name.StartsWith('/') -or $name -match '[\x00-\x1f\x7f:*?"<>|]') { throw 'Invalid archive path' }
      $directory = $name.EndsWith('/')
      $parts = $name.TrimEnd('/').Split('/')
      foreach ($part in $parts) {
        if (-not $part -or $part -eq '.' -or $part -eq '..' -or $part.EndsWith('.') -or $part.EndsWith(' ') -or $part -match '^(?i:CON|PRN|AUX|NUL|COM[0-9]|LPT[0-9])(?:\.|$)') { throw 'Unsafe archive component' }
      }
      if ($name.Length -gt 220) { throw 'Archive path too long' }
      if (-not $rootName) { $rootName = $parts[0] }
      if ($parts[0] -cne $rootName -or (-not $directory -and $parts.Count -lt 2)) { throw 'Archive must have one source root' }
      $normalized = $name.TrimEnd('/').ToLowerInvariant()
      if ($seen.ContainsKey($normalized)) { throw 'Duplicate or case-colliding archive path' }
      $seen[$normalized] = $directory
      $mode = (($entry.ExternalAttributes -shr 16) -band 61440)
      if ($mode -ne 0 -and $mode -ne 32768 -and $mode -ne 16384) { throw 'Archive links/special entries refused' }
      if (($entry.ExternalAttributes -band 1024) -ne 0) { throw 'Archive reparse entry refused' }
      if ($parts.Count -gt 1) {
        $top = $parts[1].ToLowerInvariant()
        if ($top -match '^(\.env(?:\..*)?|\.git|models|backups|certs|cache-tls|ha-out|\.upgrade-state|\.hexthings-version|install-credentials\.txt|install\.log|upgrade\.log|images\.tar\.gz|sha256sums)$' -and $top -ne '.env.example') { throw 'Runtime/private archive path refused' }
      }
      $total += $entry.Length
      if ($entry.Length -gt 134217728 -or $total -gt 536870912) { throw 'Expanded archive exceeds size limit' }
      if (-not $directory) { $files += $name }
    }
    foreach ($name in $seen.Keys) {
      $parent = $name
      while ($parent.Contains('/')) {
        $parent = $parent.Substring(0, $parent.LastIndexOf('/'))
        if ($seen.ContainsKey($parent) -and -not $seen[$parent]) { throw 'Archive file/directory collision' }
      }
    }
    foreach ($required in @('docker-compose.yml','.env.example','scripts/install.ps1','scripts/hexthings.ps1')) {
      $key = ($rootName + '/' + $required).ToLowerInvariant()
      if (-not $seen.ContainsKey($key) -or $seen[$key]) { throw 'Archive missing required source files' }
    }
    # Refuse destination links before backup/copy. This is not a lock against a
    # concurrent local administrator changing the tree after this check.
    $dest = [IO.Path]::GetFullPath($Destination)
    foreach ($name in $files) {
      $relative = $name.Substring($rootName.Length + 1)
      $target = Join-Path $dest $relative
      $node = $target
      while ($node) {
        $item = Get-Item -LiteralPath $node -Force -ErrorAction SilentlyContinue
        if ($null -ne $item) {
          if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw 'Destination link refused' }
          if ($node -ne $target -and -not $item.PSIsContainer) { throw 'Destination parent is a file' }
        }
        $next = Split-Path -Parent $node
        if ($next -eq $node) { break }; $node = $next
      }
      if (Test-Path -LiteralPath $target -PathType Container) { throw 'Destination directory conflicts with source file' }
    }
    $source = Join-Path $stage 'extracted'
    [void][IO.Directory]::CreateDirectory($source)
    foreach ($entry in $archive.Entries) {
      if ($entry.FullName.EndsWith('/')) { continue }
      $relative = $entry.FullName.Substring($rootName.Length + 1)
      $target = Join-Path $source $relative
      [void][IO.Directory]::CreateDirectory((Split-Path -Parent $target))
      [IO.Compression.ZipFileExtensions]::ExtractToFile($entry, $target, $false)
    }
    return [pscustomobject]@{ Stage=$stage; Source=$source; Digest=$digest }
  } catch {
    if ($archive) { $archive.Dispose(); $archive = $null }
    Remove-Item -LiteralPath $stage -Recurse -Force -ErrorAction SilentlyContinue
    throw 'Patch preflight refused: invalid/unsafe archive or destination. No backup/source change attempted.'
  } finally { if ($archive) { $archive.Dispose() } }
}
