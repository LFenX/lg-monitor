[CmdletBinding()]
param(
    [string]$MonitorHome = '',
    [string]$LgmonPath = '',
    [ValidateSet('Preflight', 'Snapshot', 'Delivery', 'History', 'Resources')]
    [string]$Mode = 'Preflight',
    [switch]$CheckOnly,
    [string]$CollectorID = '',
    [string]$TargetID = '',
    [string]$RuleID = '',
    [string]$NotifierID = '',
    [ValidateSet('firing', 'resolved')]
    [string]$State = 'firing',
    [ValidateSet('Process', 'Offline')]
    [string]$Scenario = 'Process',
    [string]$FromUtc = '',
    [string]$ToUtc = ''
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Invoke-M1CLI {
    param([string]$Executable, [string[]]$Arguments, [switch]$Raw)
    $output = @(& $Executable @Arguments)
    if ($LASTEXITCODE -ne 0) { throw ('lgmon 只读命令失败，退出码 {0}。' -f $LASTEXITCODE) }
    $text = $output -join [Environment]::NewLine
    if ($Raw) { return $text }
    try { return ConvertFrom-Json -InputObject $text }
    catch { throw 'lgmon 没有返回有效 JSON。' }
}

function ConvertTo-M1Time {
    param([string]$Value, [string]$Name)
    # An explicit offset keeps the A/B clock comparison reproducible.
    if ($Value -notmatch '(Z|[+-]\d\d:\d\d)$') { throw ("{0} 必须是带时区的 ISO 8601 时间。" -f $Name) }
    $parsed = [DateTimeOffset]::MinValue
    if (-not [DateTimeOffset]::TryParse($Value, [Globalization.CultureInfo]::InvariantCulture, [Globalization.DateTimeStyles]::None, [ref]$parsed)) {
        throw ("{0} 时间无效。" -f $Name)
    }
    return $parsed.ToUnixTimeMilliseconds()
}

function ConvertTo-M1Literal {
    param([string]$Value)
    if ($Value.Length -gt 255 -or $Value -match '[\x00-\x1f\x7f]') { throw '标识不能包含控制字符，长度不能超过 255。' }
    return "'" + $Value.Replace("'", "''") + "'"
}

function Invoke-M1Query {
    param([string]$Executable, [string]$MonitorHome, [string]$SQL, [string]$CollectorID, [long]$From, [long]$To)
    $arguments = @('query', $SQL, '--home', $MonitorHome, '--json', '--from', [string]$From, '--to', [string]$To)
    if ($CollectorID) { $arguments += @('--collector', $CollectorID) }
    $result = Invoke-M1CLI $Executable $arguments
    if ($result.truncated) { throw '查询结果被截断，请缩小时间范围后重新验收。' }
    foreach ($row in $result.rows) {
        if ($row.Count -ne $result.columns.Count) { throw '查询列与行不一致。' }
        $item = [ordered]@{}
        for ($index = 0; $index -lt $result.columns.Count; $index++) { $item[$result.columns[$index]] = $row[$index] }
        [PSCustomObject]$item
    }
}

function Invoke-M1Acceptance {
    param(
        [string]$MonitorHome = '', [string]$LgmonPath = '',
        [ValidateSet('Preflight', 'Snapshot', 'Delivery', 'History', 'Resources')]
        [string]$Mode = 'Preflight', [switch]$CheckOnly,
        [string]$CollectorID = '', [string]$TargetID = '', [string]$RuleID = '', [string]$NotifierID = '',
        [ValidateSet('firing', 'resolved')][string]$State = 'firing',
        [ValidateSet('Process', 'Offline')][string]$Scenario = 'Process',
        [string]$FromUtc = '', [string]$ToUtc = '',
        [scriptblock]$Now = { [DateTimeOffset]::UtcNow }
    )
    if (-not $MonitorHome) { throw '请用 -MonitorHome 指定 B 机监控器的实际 home。' }
    $MonitorHome = [IO.Path]::GetFullPath($MonitorHome)
    if (-not $LgmonPath) { $LgmonPath = Join-Path (Split-Path -Parent $PSScriptRoot) 'bin\lgmon.exe' }
    $LgmonPath = [IO.Path]::GetFullPath($LgmonPath)
    if (-not (Test-Path -LiteralPath $LgmonPath -PathType Leaf)) { throw '找不到 lgmon 程序，请先构建或用 -LgmonPath 指定。' }
    if (-not (Test-Path -LiteralPath (Join-Path $MonitorHome 'config') -PathType Container)) { throw '监控器 config 目录不存在。' }
    if ($CheckOnly) {
        if ($Mode -ne 'Preflight') { throw '-CheckOnly 只能用于 Preflight。' }
        $Mode = 'Preflight'
    }
    $at = (& $Now).ToUniversalTime()
    $checked = Invoke-M1CLI $LgmonPath @('config', 'validate', '--home', $MonitorHome, '--json')
    if (-not $checked.ok) { throw '监控器配置校验未通过。' }
    $status = Invoke-M1CLI $LgmonPath @('status', '--home', $MonitorHome, '--json')
    $report = [ordered]@{
        mode = $Mode
        checkedAtUtc = $at.ToString('o')
        version = (Invoke-M1CLI $LgmonPath @('version') -Raw)
        monitorHome = $MonitorHome
        automaticStatus = 'Passed'
        manualAcceptance = 'Pending'
        databaseAvailable = [bool]$status.available
        evidence = $null
    }
    if ($Mode -eq 'Preflight') {
        $report.evidence = '配置已校验；数据库存在不等于在线、配对或人工验收通过。'
        return [PSCustomObject]$report
    }
    if (-not $status.available) { throw '监控器没有可读数据，请等待真实监控器写入后再检查。' }
    if ($Mode -eq 'Snapshot') {
        $report.evidence = $status
        return [PSCustomObject]$report
    }
    if (-not $CollectorID) { throw '该模式必须指定 -CollectorID。' }
    $target = ConvertTo-M1Literal $TargetID
    $rule = ConvertTo-M1Literal $RuleID
    $notifier = ConvertTo-M1Literal $NotifierID
    $from = ConvertTo-M1Time $FromUtc 'FromUtc'
    $to = $at.ToUnixTimeMilliseconds()
    if ($ToUtc) { $to = ConvertTo-M1Time $ToUtc 'ToUtc' }
    if ($from -ge $to -or $to -gt $at.ToUnixTimeMilliseconds()) { throw '时间范围必须已发生，且开始早于结束。' }
    $queryArguments = @{ Executable = $LgmonPath; MonitorHome = $MonitorHome; CollectorID = $CollectorID; From = $from; To = $to }
    if ($Mode -eq 'Delivery') {
        if (-not $RuleID -or -not $NotifierID) { throw 'Delivery 必须指定 -RuleID 和 -NotifierID。' }
        if ($Scenario -eq 'Process' -and -not $TargetID) { throw 'Process 必须指定 -TargetID。' }
        if ($Scenario -eq 'Offline' -and ($TargetID -or $State -ne 'firing')) { throw 'Offline 只检查无目标的采集器失联触发；恢复请用 Snapshot 核对。' }
        $seconds = 15
        if ($Scenario -eq 'Offline') { $seconds = 35; $target = "'@collector'" }
        # Join the original transition, so reminders and pause catch-up cannot masquerade as delivery.
        $sql = @"
SELECT n.alert_id,n.event_key,n.ts AS submitted_at,e.ts AS transition_at,n.template
FROM notifications n JOIN alerts a ON a.id=n.alert_id
JOIN alert_events e ON e.alert_id=n.alert_id AND e.event_key=n.event_key
WHERE a.collector_id=:collector AND a.target_id=$target AND a.rule_id=$rule
AND n.notifier_id=$notifier AND n.state='$State' AND e.kind='$State'
AND n.ok=1 AND n.skipped=0 AND e.ts>=:from AND n.ts>=:from AND n.ts<=:to
ORDER BY n.ts,n.id LIMIT 1
"@
        $rows = @(Invoke-M1Query @queryArguments -SQL $sql)
        $report.automaticStatus = 'Pending'
        $delay = $null
        if ($rows.Count -gt 0) {
            $delay = [long]$rows[0].submitted_at - $from
            $report.automaticStatus = 'Failed'
            if ($delay -le $seconds * 1000) { $report.automaticStatus = 'Passed' }
        }
        elseif ($to -ge $from + $seconds * 1000) { $report.automaticStatus = 'Failed' }
        $report.evidence = [ordered]@{
            scenario = $Scenario; state = $State; actionAtUnixMS = $from
            deadlineSeconds = $seconds; submissionDelayMS = $delay; deliveries = $rows
            note = 'submitted_at 是渠道调用返回后记录的监控器时间；真实飞书收件和两机时钟由本人确认。'
        }
    }
    elseif ($Mode -eq 'History') {
        if (-not $TargetID -or -not $ToUtc) { throw 'History 必须指定 -TargetID、-FromUtc 和 -ToUtc。' }
        $sql = @"
WITH history AS (
 SELECT ts,state,lag(state) OVER (ORDER BY ts) AS previous
 FROM target_status WHERE collector_id=:collector AND target_id=$target AND ts>=:from AND ts<=:to
)
SELECT ts,state FROM history WHERE previous IS NULL OR state<>previous ORDER BY ts
"@
        $rows = @(Invoke-M1Query @queryArguments -SQL $sql)
        $down = $false
        $recovered = $false
        foreach ($row in $rows) {
            if ($row.state -eq 'down') { $down = $true }
            if ($down -and $row.state -eq 'up') { $recovered = $true }
        }
        $gaps = @(Invoke-M1Query @queryArguments -SQL 'SELECT ts,kind,gap_from,gap_to,dropped FROM collector_events WHERE collector_id=:collector AND ts>=:from AND ts<=:to ORDER BY ts')
        $report.automaticStatus = 'Pending'
        if ($recovered -and $gaps.Count -eq 0) { $report.automaticStatus = 'Passed' }
        if ($gaps.Count -gt 0) { $report.automaticStatus = 'Failed' }
        $report.evidence = [ordered]@{
            fromUnixMS = $from; toUnixMS = $to; transitions = $rows; gaps = $gaps
            note = '仅证明所选历史内存在 down→up；断网持续时间、补传已追平及源时间正确需本人对照操作记录。'
        }
    }
    else {
        if (-not $TargetID -or -not $ToUtc) { throw 'Resources 必须指定自监控 -TargetID、-FromUtc 和 -ToUtc。' }
        $sql = @"
WITH samples AS (
 SELECT ts,sum(cpu) AS cpu,sum(rss) AS rss,count(*) AS processes
 FROM process_samples WHERE collector_id=:collector AND target_id=$target AND ts>=:from AND ts<:to GROUP BY ts
), gaps AS (SELECT *,ts-lag(ts) OVER (ORDER BY ts) AS gap FROM samples)
SELECT count(*) AS samples,min(ts) AS first_ts,max(ts) AS last_ts,avg(cpu) AS cpu_average_percent,
avg(rss) AS rss_average_bytes,max(rss) AS rss_max_bytes,max(gap) AS max_gap_ms,
coalesce(sum(CASE WHEN processes<>1 THEN 1 ELSE 0 END),0) AS ambiguous_samples FROM gaps
"@
        $rows = @(Invoke-M1Query @queryArguments -SQL $sql)
        $report.automaticStatus = 'Pending'
        $report.evidence = [ordered]@{
            fromUnixMS = $from; toUnixMS = $to; statistics = $rows
            note = '仅报告原始采样统计；24 小时覆盖、内存单位与最大/平均判定等待 Claude 在 PLAN 明确。'
        }
    }
    return [PSCustomObject]$report
}

# Dot-sourcing exposes the same evaluators to native tests without running any command.
if ($MyInvocation.InvocationName -ne '.') {
    $previousEncoding = [Console]::OutputEncoding
    try {
        [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding
        $report = Invoke-M1Acceptance @PSBoundParameters
        $report | ConvertTo-Json -Depth 24
        if ($report.automaticStatus -eq 'Failed') { exit 1 }
        if ($report.automaticStatus -eq 'Pending') { exit 2 }
    }
    finally { [Console]::OutputEncoding = $previousEncoding }
}
