param(
  [ValidateSet('run', 'stop', 'status')]
  [string]$Action = 'run',
  [string]$Avd = 'Pixel_7',
  [string]$DeviceId = 'emulator-5554',
  [string]$Package = 'com.example.atpost_app',
  [switch]$Clean
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Write-Step {
  param([string]$Message)
  Write-Host ""
  Write-Host "==> $Message" -ForegroundColor Cyan
}

function Has-Command {
  param([string]$Name)
  return $null -ne (Get-Command $Name -ErrorAction SilentlyContinue)
}

function Kill-ProcessImageBestEffort {
  param([string]$ImageName)

  try {
    $proc = Start-Process -FilePath 'taskkill.exe' `
      -ArgumentList @('/F', '/IM', $ImageName, '/T') `
      -NoNewWindow -Wait -PassThru `
      -RedirectStandardOutput "$env:TEMP\taskkill-$ImageName-out.log" `
      -RedirectStandardError "$env:TEMP\taskkill-$ImageName-err.log"

    if ($proc.ExitCode -ne 0) {
      Write-Warning "taskkill returned exit code $($proc.ExitCode) for $ImageName (continuing)."
    }
  } catch {
    Write-Warning "taskkill failed for $ImageName (continuing): $($_.Exception.Message)"
  }
}

function Get-EmulatorEntries {
  $lines = adb devices
  $entries = @()

  foreach ($line in $lines) {
    if ($line -match '^(emulator-\d+)\s+([a-z]+)$') {
      $entries += [pscustomobject]@{
        Id = $matches[1]
        State = $matches[2]
      }
    }
  }

  return $entries
}

function Get-OnlineEmulators {
  $entries = @(Get-EmulatorEntries)
  $ids = @()
  foreach ($entry in $entries) {
    if ($entry.State -eq 'device') {
      $ids += $entry.Id
    }
  }
  return $ids
}

function Get-OfflineEmulators {
  $entries = @(Get-EmulatorEntries)
  $ids = @()
  foreach ($entry in $entries) {
    if ($entry.State -eq 'offline') {
      $ids += $entry.Id
    }
  }
  return $ids
}

function Wait-For-Emulator {
  param(
    [int]$TimeoutSeconds = 120
  )

  $start = Get-Date
  while (((Get-Date) - $start).TotalSeconds -lt $TimeoutSeconds) {
    $emulators = @(Get-OnlineEmulators)
    if ($emulators.Count -gt 0) {
      return $emulators
    }
    Start-Sleep -Seconds 2
  }

  throw "No online emulator detected within $TimeoutSeconds seconds."
}

function Resolve-TargetDevice {
  param(
    [string]$PreferredId
  )

  $online = @(Get-OnlineEmulators)
  if ($online.Count -eq 0) {
    return $null
  }

  if ($online -contains $PreferredId) {
    return $PreferredId
  }

  return $online[0]
}

function Stop-Emulators {
  Write-Step "Stopping running Android emulators"
  $online = @(Get-OnlineEmulators)
  foreach ($id in $online) {
    try {
      adb -s $id emu kill | Out-Null
      Write-Host "Stopped $id"
    } catch {
      Write-Warning "Could not stop $id cleanly: $($_.Exception.Message)"
    }
  }

  # Fallback in case emulator process is stuck/offline.
  Kill-ProcessImageBestEffort -ImageName 'qemu-system-x86_64.exe'
  Kill-ProcessImageBestEffort -ImageName 'emulator.exe'

  Write-Step "Stopping app process on connected device (best effort)"
  $target = Resolve-TargetDevice -PreferredId $DeviceId
  if ($null -ne $target) {
    try {
      adb -s $target shell am force-stop $Package | Out-Null
      Write-Host "Force-stopped $Package on $target"
    } catch {
      Write-Warning "Could not force-stop package on $target"
    }
  } else {
    Write-Host "No online emulator found for app force-stop."
  }
}

if (-not (Has-Command 'flutter')) {
  throw "flutter is not in PATH."
}
if (-not (Has-Command 'adb')) {
  throw "adb is not in PATH."
}

$projectRoot = Split-Path -Parent $PSScriptRoot
Push-Location $projectRoot
try {
  switch ($Action) {
    'status' {
      Write-Step "Flutter devices"
      flutter devices

      Write-Step "ADB devices"
      adb devices

      $resolved = Resolve-TargetDevice -PreferredId $DeviceId
      if ($null -eq $resolved) {
        Write-Host ""
        Write-Host "No online emulator detected."
      } else {
        Write-Host ""
        Write-Host "Target emulator: $resolved"
      }
    }

    'stop' {
      Stop-Emulators
      Write-Step "Done"
      Write-Host "Stopped emulator/app processes."
    }

    'run' {
      Write-Step "Resetting ADB"
      adb kill-server | Out-Null
      adb start-server | Out-Null

      $alreadyOnline = @(Get-OnlineEmulators)
      if ($alreadyOnline.Count -gt 0) {
        Write-Step "Using existing online emulator(s): $($alreadyOnline -join ', ')"
      } else {
        $offline = @(Get-OfflineEmulators)
        if ($offline.Count -gt 0) {
          Write-Step "Clearing stale offline emulator(s): $($offline -join ', ')"
          foreach ($id in $offline) {
            try {
              adb -s $id emu kill | Out-Null
            } catch {
              Write-Warning "Could not stop stale $id cleanly"
            }
          }
          Kill-ProcessImageBestEffort -ImageName 'qemu-system-x86_64.exe'
          Kill-ProcessImageBestEffort -ImageName 'emulator.exe'
          Start-Sleep -Seconds 2
        }

        Write-Step "Launching emulator AVD: $Avd"
        flutter emulators --launch $Avd
      }

      Write-Step "Waiting for emulator to come online"
      $online = Wait-For-Emulator -TimeoutSeconds 150
      Write-Host "Online emulator(s): $($online -join ', ')"

      $target = Resolve-TargetDevice -PreferredId $DeviceId
      if ($null -eq $target) {
        throw "Unable to resolve target emulator."
      }
      Write-Host "Using target device: $target"

      if ($Clean) {
        Write-Step "Cleaning Flutter build artifacts"
        flutter clean
      }

      Write-Step "Fetching dependencies"
      flutter pub get

      Write-Step "Running app (hot reload: r, hot restart: R, quit: q)"
      flutter run -d $target
    }
  }
}
finally {
  Pop-Location
}
