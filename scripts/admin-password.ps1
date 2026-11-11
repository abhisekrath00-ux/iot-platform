# Shared secure new-admin input. No secret parameters, environment or logs.
function Get-NewAdminPassword([string]$Email, [bool]$FromStdin, [bool]$Unattended) {
  if ($FromStdin) {
    $pw = [Console]::ReadLine()
    if ($null -eq $pw) { throw 'Password stdin requires one line' }
  } else {
    if ($Unattended -or [Console]::IsInputRedirected) { throw 'New unattended installs require -AdminPasswordStdin; no default password is used' }
    $one=Read-Host 'Administrator password (12-128 bytes)' -AsSecureString
    $two=Read-Host 'Confirm administrator password' -AsSecureString
    $a=[IntPtr]::Zero;$b=[IntPtr]::Zero
    try {
      $a=[Runtime.InteropServices.Marshal]::SecureStringToBSTR($one)
      $b=[Runtime.InteropServices.Marshal]::SecureStringToBSTR($two)
      $pw=[Runtime.InteropServices.Marshal]::PtrToStringBSTR($a)
      if ($pw -cne [Runtime.InteropServices.Marshal]::PtrToStringBSTR($b)) { throw 'Passwords differ; no account created' }
    } finally {
      if($a -ne [IntPtr]::Zero){[Runtime.InteropServices.Marshal]::ZeroFreeBSTR($a)}
      if($b -ne [IntPtr]::Zero){[Runtime.InteropServices.Marshal]::ZeroFreeBSTR($b)}
      $one.Dispose();$two.Dispose()
    }
  }
  $length=[Text.Encoding]::UTF8.GetByteCount($pw)
  if($length -lt 12 -or $length -gt 128 -or $pw -ieq $Email -or $pw.Contains("`r") -or $pw.Contains("`n")){throw 'Password must be 12-128 bytes and not the email'}
  return $pw
}
function Invoke-NewAdminCreate([string]$Workspace,[string]$Email,[string]$Password,[string]$Log) {
  $previous=$OutputEncoding
  try {
    $OutputEncoding=New-Object System.Text.UTF8Encoding($false)
    $Password | & docker compose exec -T api /bin/tenantctl create --id $Workspace --name $Workspace --admin-email $Email --password-stdin 2>&1 | Add-Content -Path $Log
    if($LASTEXITCODE -ne 0){throw 'Could not create workspace (see install.log)'}
  } finally {$OutputEncoding=$previous}
}
