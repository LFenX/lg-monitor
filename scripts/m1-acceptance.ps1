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
    [string]$ToUtc = '',
    [Nullable[int]]$TargetCount = $null,
    [Nullable[int]]$ProbeCount = $null
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

function New-M1ResourceResult {
    param([object]$Statistics, [object[]]$Events, [long]$From, [long]$To, [int]$TargetCount, [int]$ProbeCount)
    $windowMs = $To - $From
    $intervalMs = 2000
    $expected = $windowMs / [double]$intervalMs
    # PowerShell 5.1 emits a JSON root array as one pipeline object; normalize after assignment.
    $identityRows = ConvertFrom-Json -InputObject $Statistics.identities_json
    $skippedRows = ConvertFrom-Json -InputObject $Statistics.nonpositive_json
    $identities = @($identityRows)
    $skipped = @($skippedRows)
    $gaps = @($Events | Where-Object { $_.kind -in @('data_gap', 'late_dropped') })
    $clocks = @($Events | Where-Object { $_.kind -in @('clock_skew', 'clock_synced') })
    $coverage = New-Object 'System.Collections.Generic.List[string]'
    if ($windowMs -lt 86400000) { $coverage.Add('所选窗口不足 24 小时。') }
    if ($Statistics.samples -eq 0) { $coverage.Add('窗口内没有原始进程采样。') }
    if ($identities.Count -ne 1) { $coverage.Add('窗口内必须只有一个 PID 和启动时间组成的进程身份。') }
    elseif ($identities[0].pid -le 0 -or $identities[0].createdAt -le 0 -or $identities[0].createdAt -ge $From) {
        $coverage.Add('无法确认该进程在窗口开始前已经启动。')
    }
    if ($Statistics.ambiguous_samples -gt 0) { $coverage.Add('存在同一时间匹配多个进程的采样。') }
    if ($null -eq $Statistics.first_ts -or $Statistics.first_ts -gt $From + 10000 -or $null -eq $Statistics.last_ts -or $Statistics.last_ts -lt $To - 10000) {
        $coverage.Add('首末采样未覆盖窗口边界的 10 秒范围。')
    }
    if ($null -ne $Statistics.max_gap_ms -and $Statistics.max_gap_ms -gt 30000) { $coverage.Add('相邻采样存在超过 30 秒的间隔。') }
    if ($Statistics.samples -lt $expected * 0.95) { $coverage.Add('原始采样不足按 2 秒周期计算的预期条数的 95%。') }
    if ($gaps.Count -gt 0) { $coverage.Add('窗口内存在 data_gap 或 late_dropped 数据缺口。') }
    if ($null -eq $Statistics.cpu_average_percent) { $coverage.Add('没有可计算 CPU 均值的正时间间隔。') }
    $reasons = New-Object 'System.Collections.Generic.List[string]'
    foreach ($reason in $coverage) { $reasons.Add($reason) }
    $state = 'Pending'
    if ($null -ne $Statistics.rss_max_bytes -and $Statistics.rss_max_bytes -ge 41943040) {
        $state = 'Failed'
        $reasons.Add('原始采样工作集达到或超过 41943040 字节。')
    }
    if ($coverage.Count -eq 0) {
        if ($Statistics.cpu_average_percent -ge 1.0) {
            $state = 'Failed'
            $reasons.Add('覆盖完整，但按时间加权的 CPU 平均值达到或超过 1%。')
        }
        elseif ($state -ne 'Failed') { $state = 'Passed' }
    }
    [PSCustomObject]@{
        automaticStatus = $state
        evidence = [ordered]@{
            fromUnixMS = $From; toUnixMS = $To; windowMs = $windowMs; targetIntervalMs = $intervalMs
            samples = $Statistics.samples; expectedSamples = $expected; firstSampleAt = $Statistics.first_ts; lastSampleAt = $Statistics.last_ts
            maxGapMs = $Statistics.max_gap_ms; identities = $identities; ambiguousSamples = $Statistics.ambiguous_samples
            gapEvents = $gaps; clockEvents = $clocks; nonPositiveIntervals = $skipped
            cpuAveragePercent = $Statistics.cpu_average_percent; rssMaxBytes = $Statistics.rss_max_bytes; rssMaxAt = $Statistics.rss_max_at
            rssAverageBytes = $Statistics.rss_average_bytes; rssFirstHourAverageBytes = $Statistics.rss_first_hour_average_bytes
            rssLastHourAverageBytes = $Statistics.rss_last_hour_average_bytes
            thresholds = @{ cpuPercent = 1; rssBytes = 41943040 }; reasons = @($reasons.ToArray())
            targetCount = $TargetCount; probeCount = $ProbeCount; loadSource = 'operator'
            note = '只使用原始采样；非正间隔未计入 CPU。实际分配数量、采集器服务身份和日常负载仍需本人确认。'
        }
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
        [Nullable[int]]$TargetCount = $null, [Nullable[int]]$ProbeCount = $null,
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
        $events = @(Invoke-M1Query @queryArguments -SQL 'SELECT ts,kind,gap_from,gap_to,dropped FROM collector_events WHERE collector_id=:collector AND ts>=:from AND ts<=:to ORDER BY ts')
        $gaps = @($events | Where-Object { $_.kind -in @('data_gap', 'late_dropped') })
        $clockEvents = @($events | Where-Object { $_.kind -in @('clock_skew', 'clock_synced') })
        $report.automaticStatus = 'Pending'
        if ($recovered -and $gaps.Count -eq 0) { $report.automaticStatus = 'Passed' }
        if ($gaps.Count -gt 0) { $report.automaticStatus = 'Failed' }
        $report.evidence = [ordered]@{
            fromUnixMS = $from; toUnixMS = $to; transitions = $rows; gaps = $gaps; clockEvents = $clockEvents
            note = '仅证明所选历史内存在 down→up；断网持续时间、补传已追平及源时间正确需本人对照操作记录。'
        }
    }
    else {
        if (-not $TargetID -or -not $ToUtc) { throw 'Resources 必须指定自监控 -TargetID、-FromUtc 和 -ToUtc。' }
        if ($null -eq $TargetCount -or $TargetCount -lt 2 -or $null -eq $ProbeCount -or $ProbeCount -lt 0) {
            throw 'Resources 请用 -TargetCount 和 -ProbeCount 填写 A 的实际已应用目标数、探测数；目标至少两个，探测数不能为负。'
        }
        # Ingestion order retains clock rollback; sorting by timestamp would hide excluded intervals.
        $sql = @"
WITH samples AS (
 SELECT rowid AS sample_id,ts,pid,created_at,cpu,rss
 FROM process_samples WHERE collector_id=:collector AND target_id=$target AND ts>=:from AND ts<:to
), ordered AS (SELECT *,lag(ts) OVER (ORDER BY sample_id) AS previous_ts FROM samples)
SELECT count(*) AS samples,min(ts) AS first_ts,max(ts) AS last_ts,
 sum(CASE WHEN ts>previous_ts THEN cpu*(ts-previous_ts) END) / nullif(sum(CASE WHEN ts>previous_ts THEN ts-previous_ts END),0) AS cpu_average_percent,
 avg(rss) AS rss_average_bytes,max(rss) AS rss_max_bytes,max(ts-previous_ts) AS max_gap_ms,
 avg(CASE WHEN ts<:from+3600000 THEN rss END) AS rss_first_hour_average_bytes,
 avg(CASE WHEN ts>=:to-3600000 THEN rss END) AS rss_last_hour_average_bytes,
 (SELECT min(ts) FROM samples WHERE rss=(SELECT max(rss) FROM samples)) AS rss_max_at,
 (SELECT count(*) FROM (SELECT ts FROM samples GROUP BY ts HAVING count(DISTINCT printf('%d:%d',pid,created_at))>1)) AS ambiguous_samples,
 (SELECT json_group_array(json_object('pid',pid,'createdAt',created_at)) FROM (SELECT DISTINCT pid,created_at FROM samples)) AS identities_json,
 (SELECT json_group_array(json_object('sample',sample_id,'ts',ts,'previousTs',previous_ts,'deltaMs',ts-previous_ts))
  FROM ordered WHERE previous_ts IS NOT NULL AND ts<=previous_ts) AS nonpositive_json
FROM ordered
"@
        $rows = @(Invoke-M1Query @queryArguments -SQL $sql)
        $events = @(Invoke-M1Query @queryArguments -SQL 'SELECT ts,kind,gap_from,gap_to,dropped FROM collector_events WHERE collector_id=:collector AND ts>=:from AND ts<:to ORDER BY ts')
        $resource = New-M1ResourceResult -Statistics $rows[0] -Events $events -From $from -To $to -TargetCount $TargetCount -ProbeCount $ProbeCount
        $report.automaticStatus = $resource.automaticStatus
        $report.evidence = $resource.evidence
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
