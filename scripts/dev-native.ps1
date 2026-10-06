<#
Local development on Windows without Docker.

Runs a throwaway PostgreSQL cluster and an isolated Redis-compatible server
under .data/ (gitignored), then the API or the integration tests against them.
It uses the PostgreSQL and Memurai/Redis binaries already installed on the
machine and never touches an existing server: its own ports, its own data.

The variables below are set in the process, and the process environment wins
over .env, so the DATABASE_URL in .env (possibly a hosted database) is never
used or migrated by anything started from here.

  scripts\dev-native.ps1 up       start Postgres (:5433) and Redis (:6380); created on first use
  scripts\dev-native.ps1 api      run the API against them, in the foreground (:8080, loopback only)
  scripts\dev-native.ps1 test     run the Postgres integration tests against a separate database
  scripts\dev-native.ps1 status
  scripts\dev-native.ps1 down     stop both

Environment: PG_BIN overrides where the PostgreSQL binaries are looked up.
Solana: .data/solana-devnet.json, if present, is the devnet wallet; with
SOLANA_ALLOW_MAINNET=yes, .data/solana-mainnet.json is the mainnet wallet.
#>
param(
    [Parameter(Position = 0)]
    [ValidateSet('up', 'down', 'api', 'test', 'status')]
    [string]$Cmd = 'status'
)

$ErrorActionPreference = 'Stop'

$Root     = Split-Path -Parent $PSScriptRoot
$Data     = Join-Path $Root '.data'
$PgDir    = Join-Path $Data 'pg'
$RedisDir = Join-Path $Data 'redis'
$KeyFile  = Join-Path $Data 'local-master.key'
$PgPort   = 5433
$RedisPort = 6380
$ApiAddr  = '127.0.0.1:8080'

function Find-PgBin {
    if ($env:PG_BIN -and (Test-Path (Join-Path $env:PG_BIN 'initdb.exe'))) { return $env:PG_BIN }
    $svc = Get-CimInstance Win32_Service -Filter "Name LIKE 'postgresql%'" -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($svc -and $svc.PathName -match '"([^"]+)\\pg_ctl\.exe"') { return $Matches[1] }
    $found = Get-ChildItem 'C:\Program Files\PostgreSQL\*\bin\initdb.exe' -ErrorAction SilentlyContinue |
        Sort-Object FullName -Descending | Select-Object -First 1
    if ($found) { return $found.DirectoryName }
    throw 'PostgreSQL binaries not found. Install PostgreSQL, or set PG_BIN to its bin directory.'
}

function Find-Redis {
    $memurai = 'C:\Program Files\Memurai\memurai.exe'
    if (Test-Path $memurai) { return $memurai }
    $g = Get-Command redis-server -ErrorAction SilentlyContinue
    if ($g) { return $g.Source }
    return $null
}

function Test-PgReady([string]$bin) {
    & (Join-Path $bin 'pg_isready.exe') -h 127.0.0.1 -p $PgPort | Out-Null
    return ($LASTEXITCODE -eq 0)
}

function Test-PortOpen([int]$port) {
    return [bool](Get-NetTCPConnection -State Listen -LocalPort $port -ErrorAction SilentlyContinue)
}

function New-MasterKeyOnce {
    if (Test-Path $KeyFile) { return }
    New-Item -ItemType Directory -Force $Data | Out-Null
    $bytes = New-Object byte[] 32
    $rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    $rng.GetBytes($bytes)
    $rng.Dispose()
    [Convert]::ToBase64String($bytes) | Set-Content -Path $KeyFile -Encoding ascii -NoNewline
}

function Set-LocalEnv([string]$database) {
    New-MasterKeyOnce
    $env:DATABASE_URL       = "postgres://algebra@127.0.0.1:$PgPort/${database}?sslmode=disable"
    $env:REDIS_ADDR         = "127.0.0.1:$RedisPort"
    $env:ALGEBRA_MASTER_KEY = (Get-Content $KeyFile -Raw).Trim()
    $env:HTTP_ADDR          = $ApiAddr
    Set-SolanaWallets
}

# Wallets under .data/ become the console's devnet and mainnet rails, unless
# the environment already configures Solana. Devnet: create one with
#   go run ./cmd/solana-wallet -new -out .data/solana-devnet.json
# and fund it at faucet.circle.com. Mainnet pays real money, so its wallet
# (.data/solana-mainnet.json) is only used when SOLANA_ALLOW_MAINNET=yes is
# already set.
function Set-SolanaWallets {
    if ($env:SOLANA_CLUSTER) { return }
    $devnet = Join-Path $Data 'solana-devnet.json'
    if (-not $env:SOLANA_DEVNET_KEYPAIR -and -not $env:SOLANA_DEVNET_KEYPAIR_FILE -and (Test-Path $devnet)) {
        $env:SOLANA_DEVNET_KEYPAIR_FILE = $devnet
    }
    $mainnet = Join-Path $Data 'solana-mainnet.json'
    if ($env:SOLANA_ALLOW_MAINNET -eq 'yes' -and -not $env:SOLANA_MAINNET_KEYPAIR -and -not $env:SOLANA_MAINNET_KEYPAIR_FILE -and (Test-Path $mainnet)) {
        $env:SOLANA_MAINNET_KEYPAIR_FILE = $mainnet
    }
}

function Start-Postgres {
    $bin = Find-PgBin
    if (-not (Test-Path (Join-Path $PgDir 'PG_VERSION'))) {
        New-Item -ItemType Directory -Force $Data | Out-Null
        # trust auth is fine here: loopback only, throwaway data.
        & (Join-Path $bin 'initdb.exe') -D $PgDir -U algebra -A trust -E UTF8 --locale=C | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'initdb failed' }
    }
    if (-not (Test-PgReady $bin)) {
        # Start-Process with redirected output, so the server does not inherit (and hold open) this shell's pipes.
        Start-Process -FilePath (Join-Path $bin 'postgres.exe') -WindowStyle Hidden `
            -ArgumentList @('-D', "`"$PgDir`"", '-p', "$PgPort", '-c', 'listen_addresses=127.0.0.1') `
            -RedirectStandardError (Join-Path $Data 'pg.log') | Out-Null
        $ready = $false
        for ($i = 0; $i -lt 60 -and -not $ready; $i++) {
            Start-Sleep -Milliseconds 500
            $ready = Test-PgReady $bin
        }
        if (-not $ready) { throw "Postgres did not come up; see $Data\pg.log" }
    }
    # algebra: the API. algebra_test: the integration tests, which reset tables.
    foreach ($db in 'algebra', 'algebra_test') {
        $has = & (Join-Path $bin 'psql.exe') -h 127.0.0.1 -p $PgPort -U algebra -d postgres -Atc "select 1 from pg_database where datname='$db'"
        if (-not $has) { & (Join-Path $bin 'createdb.exe') -h 127.0.0.1 -p $PgPort -U algebra $db }
    }
    Write-Host "postgres  ready on 127.0.0.1:$PgPort (databases: algebra, algebra_test)"
}

function Start-Redis {
    $pidFile = Join-Path $RedisDir 'server.pid'
    if (Test-PortOpen $RedisPort) { Write-Host "redis     already on 127.0.0.1:$RedisPort"; return }
    $exe = Find-Redis
    if (-not $exe) {
        Write-Host 'redis     not found, skipped (the API runs without rate limiting and locks)'
        return
    }
    New-Item -ItemType Directory -Force $RedisDir | Out-Null
    $p = Start-Process -FilePath $exe -WindowStyle Hidden -PassThru `
        -ArgumentList @('--port', "$RedisPort", '--bind', '127.0.0.1', '--save', '""', '--appendonly', 'no', '--dir', "`"$RedisDir`"") `
        -RedirectStandardOutput (Join-Path $RedisDir 'out.log') -RedirectStandardError (Join-Path $RedisDir 'err.log')
    $p.Id | Set-Content -Path $pidFile -Encoding ascii
    for ($i = 0; $i -lt 20 -and -not (Test-PortOpen $RedisPort); $i++) { Start-Sleep -Milliseconds 500 }
    if (-not (Test-PortOpen $RedisPort)) { throw "Redis did not come up; see $RedisDir\err.log" }
    Write-Host "redis     ready on 127.0.0.1:$RedisPort (no persistence)"
}

function Stop-Everything {
    $bin = Find-PgBin
    if (Test-Path (Join-Path $PgDir 'postmaster.pid')) {
        & (Join-Path $bin 'pg_ctl.exe') -D $PgDir -m fast stop | Out-Null
        Write-Host 'postgres  stopped'
    }
    $pidFile = Join-Path $RedisDir 'server.pid'
    if (Test-Path $pidFile) {
        $id = [int](Get-Content $pidFile -Raw).Trim()
        # Only stop a process that is demonstrably this script's instance, never the machine's own Redis.
        $proc = Get-CimInstance Win32_Process -Filter "ProcessId=$id" -ErrorAction SilentlyContinue
        if ($proc -and $proc.CommandLine -match "--port\s+$RedisPort\b") {
            Stop-Process -Id $id -Force
            Write-Host 'redis     stopped'
        }
        Remove-Item $pidFile -Force
    }
}

switch ($Cmd) {
    'up' {
        Start-Postgres
        Start-Redis
    }
    'down' { Stop-Everything }
    'status' {
        $bin = Find-PgBin
        Write-Host ("postgres  {0}" -f $(if (Test-PgReady $bin) { "up on 127.0.0.1:$PgPort" } else { 'down' }))
        Write-Host ("redis     {0}" -f $(if (Test-PortOpen $RedisPort) { "up on 127.0.0.1:$RedisPort" } else { 'down' }))
        Write-Host ("api       {0}" -f $(if (Test-PortOpen 8080) { "listening on :8080" } else { 'down' }))
    }
    'api' {
        Set-LocalEnv 'algebra'
        Set-Location $Root
        go run ./cmd/api
    }
    'test' {
        Set-LocalEnv 'algebra_test'
        Set-Location $Root
        go test ./internal/platform/postgres/... ./test/e2e/... -count=1 -v
    }
}
