[CmdletBinding()]
param(
    [string]$SourceHome = '',
    [Parameter(Mandatory = $true)]
    [string]$Notifier,
    [string]$LgmonPath = '',
    [switch]$CheckOnly,
    [switch]$SplitRoles,
    [switch]$SendConfirmed
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
if ($SourceHome -eq '') { $SourceHome = Join-Path $projectRoot 'dev-home' }
if ($LgmonPath -eq '') { $LgmonPath = Join-Path $projectRoot 'bin\lgmon.exe' }
$ownedRoot = Join-Path ([IO.Path]::GetTempPath()) ('lgmon-m0-accept-' + [Guid]::NewGuid().ToString('N'))
$demoProcess = $null
$monitorProcess = $null
$collectorProcess = $null
$previousCGO = $env:CGO_ENABLED
$previousGOPATH = $env:GOPATH
$previousGOCACHE = $env:GOCACHE
$previousTelemetry = $env:GOTELEMETRY
$total = [Diagnostics.Stopwatch]::StartNew()

function Report-Step([string]$Message, [Diagnostics.Stopwatch]$Timer) {
    Write-Host ('[通过] {0}（{1:N2} 秒）' -f $Message, $Timer.Elapsed.TotalSeconds)
}

function Invoke-Json([string]$Executable, [string[]]$Arguments) {
    $lines = @(& $Executable @Arguments)
    if ($LASTEXITCODE -ne 0) { throw ('子命令执行失败，退出码 {0}' -f $LASTEXITCODE) }
    return (($lines -join "`n") | ConvertFrom-Json)
}

function Stop-OwnedProcess($Process) {
    if ($null -ne $Process) {
        try {
            if (-not $Process.HasExited) {
                $Process.Kill()
                if (-not $Process.WaitForExit(5000)) { throw '自建进程未能在 5 秒内退出。' }
            }
        }
        finally { $Process.Dispose() }
    }
}

function Assert-MonitorRunning {
    if ($monitorProcess.HasExited) { throw '验收监控进程已退出，请查看本次错误输出。' }
    if ($null -ne $collectorProcess -and $collectorProcess.HasExited) { throw '验收采集器进程已退出。' }
}

function Wait-Delivery([string]$State, [string]$AlertID, [Diagnostics.Stopwatch]$Timer) {
    $lastFailure = ''
    while ($Timer.Elapsed.TotalSeconds -lt 15) {
        Assert-MonitorRunning
        try {
            $snapshot = Invoke-Json $LgmonPath @('alerts', '--all', '--json', '--home', $testHome)
            $alerts = @($snapshot.alerts | Where-Object { $_.targetId -eq $selection.targetId -and $_.state -eq $State -and ($AlertID -eq '' -or $_.id -eq $AlertID) })
            if ($alerts.Count -gt 0) {
                $alert = $alerts[0]
                $receipt = Invoke-Json $demoExecutable @('audit', '--home', $testHome, '--alert-id', $alert.id, '--state', $State, '--notifier', $Notifier)
                if ($receipt.ok -and $Timer.Elapsed.TotalSeconds -le 15) {
                    Report-Step ($State + ' 已保存，当前状态和渠道的发送审计成功') $Timer
                    return $alert
                }
            }
        }
        catch { $lastFailure = $_.Exception.Message }
        Start-Sleep -Milliseconds 200
    }
    throw ('15 秒内未取得 {0} 告警与成功通知记录。最近查询错误：{1}' -f $State, $lastFailure)
}

Push-Location $projectRoot
try {
    if (-not $CheckOnly -and -not $SendConfirmed) {
        throw '实际验收会向所选渠道发送两张卡片。确认它是测试群后，请加 -SendConfirmed；只检查配置请加 -CheckOnly。'
    }
    $SourceHome = [IO.Path]::GetFullPath($SourceHome)
    $LgmonPath = [IO.Path]::GetFullPath($LgmonPath)
    if (-not (Test-Path -LiteralPath $LgmonPath -PathType Leaf)) { throw '请先运行 scripts\build.ps1。' }
    $goCommand = Get-Command go -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    $goExecutable = if ($goCommand) { $goCommand.Source } else { Join-Path $env:ProgramFiles 'Go\bin\go.exe' }
    if (-not (Test-Path -LiteralPath $goExecutable -PathType Leaf)) { throw '请先安装标准版 Go。' }
    New-Item -ItemType Directory -Path $ownedRoot | Out-Null
    $demoExecutable = Join-Path $ownedRoot 'lgmon-m0-demo.exe'
    $testHome = Join-Path $ownedRoot 'home'
    $env:CGO_ENABLED = '0'
    $env:GOPATH = Join-Path ([IO.Path]::GetTempPath()) 'lgmon-go'
    $env:GOCACHE = Join-Path ([IO.Path]::GetTempPath()) 'lgmon-go-build-cache'
    $env:GOTELEMETRY = 'off'
    $step = [Diagnostics.Stopwatch]::StartNew()
    & $goExecutable build -trimpath -o $demoExecutable './examples/m0demo'
    if ($LASTEXITCODE -ne 0) { throw '构建自建演示程序失败。' }
    $selection = Invoke-Json $demoExecutable @('inspect', '--home', $SourceHome, '--notifier', $Notifier)
    Report-Step ('演示程序已构建，渠道：' + $selection.notifier + '，群名：' + $selection.chatName) $step
    if ($CheckOnly) {
        Write-Host '只读检查完成。未读取密钥、未启动监控、未发送消息；这不是 M0 人工验收通过。'
        return
    }

    $step = [Diagnostics.Stopwatch]::StartNew()
    $portLease = New-Object Net.Sockets.TcpListener([Net.IPAddress]::Loopback, 0)
    try {
        $portLease.Start()
        $port = $portLease.LocalEndpoint.Port
    }
    finally { $portLease.Stop() }
    $nonce = [Guid]::NewGuid().ToString('N')
    $selection = Invoke-Json $demoExecutable @('prepare', '--source-home', $SourceHome, '--home', $testHome, '--notifier', $Notifier, '--demo', $demoExecutable, '--port', [string]$port, '--nonce', $nonce)
    $demoArguments = @('serve', '--nonce', $nonce, '--port', [string]$port)
    $demoProcess = Start-Process -FilePath $demoExecutable -ArgumentList $demoArguments -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $ownedRoot 'demo.out') -RedirectStandardError (Join-Path $ownedRoot 'demo.err')
    $runRole = 'both'
    $monitorArguments = @()
    if ($SplitRoles) {
        $runRole = 'monitor'
        do {
            $monitorLease = New-Object Net.Sockets.TcpListener([Net.IPAddress]::Loopback, 0)
            try {
                $monitorLease.Start()
                $monitorPort = $monitorLease.LocalEndpoint.Port
            }
            finally { $monitorLease.Stop() }
        } while ($monitorPort -eq $port)
        $monitorAddress = '127.0.0.1:' + $monitorPort
        $monitorArguments = @('--listen', $monitorAddress)
    }
    $monitorArguments = @('run', '--role', $runRole, '--home', ('"{0}"' -f $testHome)) + $monitorArguments
    $monitorProcess = Start-Process -FilePath $LgmonPath -ArgumentList $monitorArguments -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $ownedRoot 'monitor.out') -RedirectStandardError (Join-Path $ownedRoot 'monitor.err')
    if ($SplitRoles) {
        $ready = $false
        while ($step.Elapsed.TotalSeconds -lt 10) {
            Assert-MonitorRunning
            $tcpClient = New-Object Net.Sockets.TcpClient
            try {
                if ($tcpClient.ConnectAsync('127.0.0.1', $monitorPort).Wait(200)) { $ready = $true; break }
            }
            catch { }
            finally { $tcpClient.Dispose() }
            Start-Sleep -Milliseconds 100
        }
        if (-not $ready) { throw '10 秒内监控器未开始监听。' }
        $collectorHome = Join-Path $ownedRoot 'collector'
        $collectorConfig = Join-Path $collectorHome 'config'
        New-Item -ItemType Directory -Path $collectorConfig -Force | Out-Null
        Copy-Item -LiteralPath (Join-Path $testHome 'config\targets.yaml') -Destination (Join-Path $collectorConfig 'targets.yaml')
        $collectorProcess = Start-Process -FilePath $LgmonPath -ArgumentList @('run', '--role', 'collector', '--home', ('"{0}"' -f $collectorHome), '--monitor', $monitorAddress) -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $ownedRoot 'collector.out') -RedirectStandardError (Join-Path $ownedRoot 'collector.err')
    }
    $isUp = $false
    while ($step.Elapsed.TotalSeconds -lt 20) {
        Assert-MonitorRunning
        if ($demoProcess.HasExited) { throw '演示服务启动失败，空闲端口已被占用或程序异常。' }
        $snapshot = Invoke-Json $LgmonPath @('status', '--json', '--home', $testHome)
        $up = @($snapshot.targets | Where-Object { $_.targetId -eq $selection.targetId -and $_.state -eq 'up' })
        if ($up.Count -gt 0) { $isUp = $true; break }
        Start-Sleep -Milliseconds 200
    }
    if (-not $isUp) { throw '20 秒内演示服务未变为 up。' }
    Report-Step '独立配置与 DPAPI 密钥已准备，演示服务 up' $step

    Stop-OwnedProcess $demoProcess
    $demoProcess = $null
    $step = [Diagnostics.Stopwatch]::StartNew()
    $firing = Wait-Delivery 'firing' '' $step

    $step = [Diagnostics.Stopwatch]::StartNew()
    $demoProcess = Start-Process -FilePath $demoExecutable -ArgumentList $demoArguments -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $ownedRoot 'recovery.out') -RedirectStandardError (Join-Path $ownedRoot 'recovery.err')
    $resolved = Wait-Delivery 'resolved' $firing.id $step
    Write-Host ('链路检查完成，告警：{0}。请在测试群核对同一告警的触发和恢复两张卡片，人工收件结果仍待确认。' -f $resolved.id)
}
catch {
    Write-Host ('[失败] {0}（总计 {1:N2} 秒）' -f $_.Exception.Message, $total.Elapsed.TotalSeconds)
    foreach ($logName in @('monitor.err', 'collector.err', 'demo.err', 'recovery.err')) {
        $logPath = Join-Path $ownedRoot $logName
        if (Test-Path -LiteralPath $logPath -PathType Leaf) { Get-Content -LiteralPath $logPath -Tail 12 | Write-Host }
    }
    throw
}
finally {
    $cleanupErrors = @()
    foreach ($ownedProcess in @($demoProcess, $collectorProcess, $monitorProcess)) {
        try { Stop-OwnedProcess $ownedProcess } catch { $cleanupErrors += $_.Exception.Message }
    }
    try {
        if (Test-Path -LiteralPath $ownedRoot) {
            $resolvedRoot = (Resolve-Path -LiteralPath $ownedRoot).ProviderPath
            $tempParent = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\') + '\'
            if (-not $resolvedRoot.StartsWith($tempParent, [StringComparison]::OrdinalIgnoreCase) -or [IO.Path]::GetFileName($resolvedRoot) -notmatch '^lgmon-m0-accept-[a-f0-9]{32}$') {
                throw '拒绝清理不属于本次验收的临时目录。'
            }
            Remove-Item -LiteralPath $resolvedRoot -Recurse -Force
        }
    }
    catch { $cleanupErrors += $_.Exception.Message }
    $env:CGO_ENABLED = $previousCGO
    $env:GOPATH = $previousGOPATH
    $env:GOCACHE = $previousGOCACHE
    $env:GOTELEMETRY = $previousTelemetry
    Pop-Location
    if ($cleanupErrors.Count -gt 0) { throw ('清理失败：' + ($cleanupErrors -join '；')) }
    Write-Host ('[通过] 自建进程和临时目录已清理（总计 {0:N2} 秒）' -f $total.Elapsed.TotalSeconds)
}
