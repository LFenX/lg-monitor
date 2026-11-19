[CmdletBinding()]
param(
    [Parameter(Mandatory=$true)]
    [string]$CompilerPath,
    [string]$EvidenceDirectory
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$temporaryRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\') + '\'
$fixtureRoot = Join-Path $temporaryRoot ('lgmon-installer-test-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $fixtureRoot | Out-Null
$previousCGO = $env:CGO_ENABLED
$previousDirectory = Get-Location
$results = @()

function Assert-TemporaryTarget([string]$Path) {
    $resolved = [IO.Path]::GetFullPath($Path)
    if (-not $resolved.StartsWith($fixtureRoot + '\', [StringComparison]::OrdinalIgnoreCase)) { throw '测试目标越出本轮独立临时目录。' }
    return $resolved
}

function Invoke-FixtureProcess([string]$Executable, [string[]]$Arguments, [bool]$Success) {
    $null = Assert-TemporaryTarget $Executable
    $quoted = @($Arguments | ForEach-Object {
        if ($_ -match '["\r\n]') { throw '测试参数包含非法引号或换行。' }
        '"' + $_ + '"'
    })
    $process = Start-Process -FilePath $Executable -ArgumentList $quoted -WindowStyle Hidden -Wait -PassThru
    if (($process.ExitCode -eq 0) -ne $Success) {
        foreach ($argument in $Arguments) {
            if ($argument.StartsWith('/LOG=', [StringComparison]::OrdinalIgnoreCase)) {
                $log = Assert-TemporaryTarget $argument.Substring(5)
                if (Test-Path -LiteralPath $log -PathType Leaf) { Get-Content -LiteralPath $log -Tail 24 | ForEach-Object { Write-Host $_ } }
            }
        }
        throw ('安装夹具退出码不符：' + $process.ExitCode + '；' + $Executable)
    }
    return $process.ExitCode
}

function New-InstallerFixture([string]$Name) {
    $root = Join-Path $fixtureRoot $Name
    New-Item -ItemType Directory -Path (Join-Path $root 'payload') | Out-Null
    $null = Assert-TemporaryTarget $root
    $permissions = [Security.AccessControl.DirectorySecurity]::new()
    $permissions.SetOwner([Security.Principal.SecurityIdentifier]::new('S-1-5-32-544'))
    $permissions.SetAccessRuleProtection($true, $false)
    foreach ($sid in @('S-1-5-18','S-1-5-32-544')) {
        $rule = [Security.AccessControl.FileSystemAccessRule]::new([Security.Principal.SecurityIdentifier]::new($sid), [Security.AccessControl.FileSystemRights]::FullControl, [Security.AccessControl.InheritanceFlags]'ContainerInherit,ObjectInherit', [Security.AccessControl.PropagationFlags]::None, [Security.AccessControl.AccessControlType]::Allow)
        $permissions.AddAccessRule($rule)
    }
    Set-Acl -LiteralPath $root -AclObject $permissions
    $payload = Join-Path $root 'payload\lgmon.exe'
    $tag = [Guid]::NewGuid().ToString('N')
    $flags = '-X "main.installerFixtureRoot={0}" -X "main.installerFixtureTag={1}"' -f $root, $tag
    & $script:goExecutable build -tags 'webui,installerfixture' -trimpath -ldflags $flags -o $payload './cmd/lgmon'
    if ($LASTEXITCODE -ne 0) { throw '安装夹具载荷构建失败。' }
    & $CompilerPath '/Q' '/DFixtureMode=1' ('/DFixtureRoot=' + $root) ('/DFixtureTag=' + $tag) ('/DPayloadFile=' + $payload) ('/DOutputDirectory=' + $root) (Join-Path $PSScriptRoot 'lgmon.iss')
    if ($LASTEXITCODE -ne 0) { throw '安装夹具编译失败。' }
    $installer = Join-Path $root ('lgmon-installer-fixture-' + $tag + '.exe')
    return [pscustomobject]@{root=$root;tag=$tag;payload=$payload;installer=$installer;program=(Join-Path $root 'program\lgmon.exe');uninstaller=(Join-Path $root 'program\unins000.exe');home=(Join-Path $root 'home');log=(Join-Path $root 'install.log')}
}

function Read-FixtureState($Case) {
    return Get-Content -LiteralPath (Join-Path $Case.root 'scm.json') -Raw | ConvertFrom-Json
}

function Invoke-FixtureInstall($Case, [string]$Role, [bool]$Success, [string[]]$Extra=@()) {
    $arguments = @('/VERYSILENT','/SUPPRESSMSGBOXES','/NORESTART','/SP-',('/LGMONROLE=' + $Role),'/LGMONCONFIRM',('/LOG=' + $Case.log)) + $Extra
    $null = Invoke-FixtureProcess $Case.installer $arguments $Success
}

function Invoke-FixtureUninstall($Case, [bool]$Success) {
    $null = Invoke-FixtureProcess $Case.uninstaller @('/VERYSILENT','/SUPPRESSMSGBOXES','/NORESTART',('/LOG=' + (Join-Path $Case.root 'uninstall.log'))) $Success
}

function Stop-FixtureChild($Child) {
    if ($null -eq $Child) { return }
    if (-not $Child.HasExited) {
        if ($Child.StartInfo.RedirectStandardInput) { $Child.StandardInput.BaseStream.Close() }
        if (-not $Child.WaitForExit(5000)) { $Child.Kill(); $Child.WaitForExit() }
    }
    $Child.Dispose()
}

function Read-FixtureLine($Child) {
    $read = $Child.StandardOutput.ReadLineAsync()
    if (-not $read.Wait(10000)) { throw '隔离 MCP/API 十秒内没有回应。' }
    if ($null -eq $read.Result) { throw '隔离 MCP/API 意外退出。' }
    return $read.Result
}

function Write-FixtureLine($Child, [string]$Line) {
    # Windows PowerShell's StandardInput writer can prepend the console's UTF-8 BOM and break JSON-RPC.
    $bytes = [Text.UTF8Encoding]::new($false).GetBytes($Line + "`n")
    $Child.StandardInput.BaseStream.Write($bytes, 0, $bytes.Length)
    $Child.StandardInput.BaseStream.Flush()
}

function Start-FixtureAPI($Case) {
    $scriptFile = Assert-TemporaryTarget (Join-Path $Case.root 'api.js')
    $source = @'
const http = require('node:http');
const server = http.createServer((req, res) => {
  res.setHeader('Content-Type', 'application/json');
  if (req.headers.authorization !== 'Bearer FAKE_INSTALLER_MCP_TOKEN') {
    res.writeHead(401); res.end('{}'); return;
  }
  res.end(req.url === '/api/v1/collectors'
    ? '[{"id":"fake-upgrade-collector","hostname":"fixture"}]'
    : '{"ok":true}');
});
server.listen(0, '127.0.0.1', () => console.log(JSON.stringify({port: server.address().port})));
setTimeout(() => { server.close(); process.exit(0); }, 600000).unref();
'@
    [IO.File]::WriteAllText($scriptFile, $source, [Text.UTF8Encoding]::new($false))
    $start = [Diagnostics.ProcessStartInfo]::new()
    $start.FileName = $script:nodeExecutable
    $start.Arguments = '"' + $scriptFile + '"'
    $start.UseShellExecute = $false
    $start.CreateNoWindow = $true
    $start.RedirectStandardOutput = $true
    $child = [Diagnostics.Process]::Start($start)
    try {
        $ready = Read-FixtureLine $child | ConvertFrom-Json
        return [pscustomobject]@{process=$child;url=('http://127.0.0.1:' + $ready.port)}
    }
    catch { Stop-FixtureChild $child; throw }
}

function Invoke-FixtureRPC($Session, [string]$Method, $Parameters) {
    $id = $Session.nextId
    $Session.nextId++
    $body = @{jsonrpc='2.0';id=$id;method=$Method;params=$Parameters} | ConvertTo-Json -Depth 10 -Compress
    Write-FixtureLine $Session.process $body
    while ($true) {
        $response = Read-FixtureLine $Session.process | ConvertFrom-Json
        if ($response.PSObject.Properties.Name -notcontains 'id' -or $response.id -ne $id) { continue }
        if ($response.PSObject.Properties.Name -contains 'error') { throw ('隔离 MCP 调用失败：' + $response.error.message) }
        return $response.result
    }
}

function Start-FixtureMCP($Case, [string]$URL) {
    $start = [Diagnostics.ProcessStartInfo]::new()
    $start.FileName = Assert-TemporaryTarget $Case.program
    $start.Arguments = 'mcp --tools all --url ' + $URL
    $start.UseShellExecute = $false
    $start.CreateNoWindow = $true
    $start.RedirectStandardInput = $true
    $start.RedirectStandardOutput = $true
    $start.RedirectStandardError = $true
    $start.StandardOutputEncoding = [Text.UTF8Encoding]::new($false)
    $start.StandardErrorEncoding = [Text.UTF8Encoding]::new($false)
    $start.EnvironmentVariables['LGMON_TOKEN'] = 'FAKE_INSTALLER_MCP_TOKEN'
    $previousInputEncoding = [Console]::InputEncoding
    try {
        # .NET Framework emits this preamble when Process.Start creates its input writer, before any write.
        [Console]::InputEncoding = [Text.UTF8Encoding]::new($false)
        $child = [Diagnostics.Process]::Start($start)
    }
    finally { [Console]::InputEncoding = $previousInputEncoding }
    $session = [pscustomobject]@{process=$child;nextId=1;stderr=$child.StandardError.ReadToEndAsync()}
    try {
        $null = Invoke-FixtureRPC $session 'initialize' @{protocolVersion='2025-06-18';capabilities=@{};clientInfo=@{name='fixture-agent';version='fake'}}
        Write-FixtureLine $child '{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}'
        return $session
    }
    catch {
        $message = $_.Exception.Message
        if ($child.HasExited -and $session.stderr.Wait(5000)) {
            $message += ' exit=' + $child.ExitCode + ': ' + $session.stderr.Result
        }
        Stop-FixtureChild $child
        throw $message
    }
}

function Assert-FixtureMCP($Session) {
    if ($Session.process.HasExited) { throw '更新结束了已连接的 MCP 进程。' }
    $reply = Invoke-FixtureRPC $Session 'tools/call' @{name='list_collectors';arguments=@{}}
    if (($reply.PSObject.Properties.Name -contains 'isError' -and $reply.isError) -or
        -not (($reply.content | ConvertTo-Json -Depth 10 -Compress).Contains('fake-upgrade-collector'))) {
        throw '更新前后的 MCP 代理调用没有返回隔离 API 的数据。'
    }
}

try {
    Set-Location -LiteralPath $projectRoot
    $env:CGO_ENABLED = '0'
    $go = Get-Command go -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($go) { $script:goExecutable = $go.Source }
    else { $script:goExecutable = Join-Path $env:ProgramFiles 'Go\bin\go.exe' }
    $CompilerPath = (Resolve-Path -LiteralPath $CompilerPath).ProviderPath
    $script:nodeExecutable = (Get-Command node.exe -CommandType Application -ErrorAction Stop | Select-Object -First 1).Source

    # These are real stdio sessions holding the installed image, while the SCM is always the fixture driver.
    $agentCase = New-InstallerFixture 'connected-agents'
    Invoke-FixtureInstall $agentCase 'both' $true
    $append = [IO.File]::Open($agentCase.program, [IO.FileMode]::Append, [IO.FileAccess]::Write)
    try { $bytes = [Text.Encoding]::ASCII.GetBytes('FAKE_OLD_AGENT_IMAGE'); $append.Write($bytes,0,$bytes.Length) }
    finally { $append.Dispose() }
    $oldHash = (Get-FileHash -LiteralPath $agentCase.program -Algorithm SHA256).Hash
    $homeFiles = @(Get-ChildItem -LiteralPath $agentCase.home -Recurse -File |
        Where-Object { $_.FullName -ne (Join-Path $agentCase.home 'data\setup\installation.json') } |
        ForEach-Object { [pscustomobject]@{path=$_.FullName;hash=(Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash} })
    $api = $agentOne = $agentTwo = $agentThree = $null
    try {
        $api = Start-FixtureAPI $agentCase
        $agentOne = Start-FixtureMCP $agentCase $api.url
        Assert-FixtureMCP $agentOne
        $writeBlocked = $false
        try { $write = [IO.File]::Open($agentCase.program, [IO.FileMode]::Open, [IO.FileAccess]::Write); $write.Dispose() }
        catch [IO.IOException] { $writeBlocked = $true }
        catch [UnauthorizedAccessException] { $writeBlocked = $true }
        if (-not $writeBlocked) { throw '测试没有复现运行中程序的写入限制。' }

        # Failed preflight, an exclusive reader, and failed service removal must keep the old installation usable.
        [IO.File]::WriteAllText((Join-Path $agentCase.root 'fault.txt'),'binding')
        Invoke-FixtureInstall $agentCase 'both' $false @('/LGMONUPGRADE')
        Remove-Item -LiteralPath (Join-Path $agentCase.root 'fault.txt')
        $reader = [IO.File]::Open($agentCase.program, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
        try { Invoke-FixtureInstall $agentCase 'both' $false @('/LGMONUPGRADE') }
        finally { $reader.Dispose() }
        [IO.File]::WriteAllText((Join-Path $agentCase.root 'fault.txt'),'remove')
        Invoke-FixtureInstall $agentCase 'both' $false @('/LGMONUPGRADE')
        Remove-Item -LiteralPath (Join-Path $agentCase.root 'fault.txt')
        if ((Get-FileHash -LiteralPath $agentCase.program -Algorithm SHA256).Hash -ne $oldHash -or
            (Read-FixtureState $agentCase).removed -ne 0 -or -not (Read-FixtureState $agentCase).installed) {
            throw '失败的更新改变了旧程序或移除了旧服务。'
        }
        Assert-FixtureMCP $agentOne

        Invoke-FixtureInstall $agentCase 'both' $true @('/LGMONUPGRADE')
        Assert-FixtureMCP $agentOne
        if ((Get-FileHash -LiteralPath $agentCase.program -Algorithm SHA256).Hash -ne (Get-FileHash -LiteralPath $agentCase.payload -Algorithm SHA256).Hash) {
            throw 'Agent 连接时安装器没有替换为新载荷。'
        }
        $agentTwo = Start-FixtureMCP $agentCase $api.url
        Invoke-FixtureInstall $agentCase 'both' $true @('/LGMONUPGRADE')
        Assert-FixtureMCP $agentOne
        Assert-FixtureMCP $agentTwo
        if (@(Get-ChildItem -LiteralPath (Split-Path -Parent $agentCase.program) -File -Filter 'lgmon.exe.previous-*').Count -ne 2) {
            throw '连续更新没有独立保留仍在使用的旧程序。'
        }
        Stop-FixtureChild $agentOne.process; $agentOne = $null
        Stop-FixtureChild $agentTwo.process; $agentTwo = $null
        Invoke-FixtureInstall $agentCase 'both' $true @('/LGMONUPGRADE')
        if (@(Get-ChildItem -LiteralPath (Split-Path -Parent $agentCase.program) -File -Filter 'lgmon.exe.previous-*').Count -ne 0) {
            throw 'Agent 断开后，下一次更新没有回收旧程序。'
        }
        foreach ($file in $homeFiles) {
            if (-not (Test-Path -LiteralPath $file.path -PathType Leaf) -or (Get-FileHash -LiteralPath $file.path -Algorithm SHA256).Hash -ne $file.hash) {
                throw ('Agent 连着更新改变了 home：' + $file.path)
            }
        }
        # An agent still connected at uninstall is closed with the rest, so no file waits for a restart.
        $agentThree = Start-FixtureMCP $agentCase $api.url
        Assert-FixtureMCP $agentThree
        Invoke-FixtureUninstall $agentCase $true
        if (-not $agentThree.process.WaitForExit(10000)) { throw '卸载没有关闭仍连着的 Agent 会话。' }
        $agentThree.process.Dispose(); $agentThree = $null
        $programDirectory = Split-Path -Parent $agentCase.program
        if (Test-Path -LiteralPath $agentCase.program) { throw '卸载后程序仍在。' }
        # Under strict mode a missing value (the good case) cannot be read as a property, so ask the registry directly.
        $pending = @([Microsoft.Win32.Registry]::GetValue('HKEY_LOCAL_MACHINE\SYSTEM\CurrentControlSet\Control\Session Manager', 'PendingFileRenameOperations', $null))
        if (@($pending | Where-Object { $_ -and $_.ToString().IndexOf($programDirectory, [StringComparison]::OrdinalIgnoreCase) -ge 0 }).Count -ne 0) { throw '卸载把程序文件留到重启后才删除。' }
        $results += [pscustomobject]@{case='connected-agents';uninstallClosesAgents=$true;realStdio=$true;proxyCallsSurvive=$true;consecutiveUpgrades=$true;failedUpgradesPreserveService=$true;homePreserved=$true;retiredImagesReclaimed=$true;realSCM=$false}
    }
    finally {
        if ($null -ne $agentOne) { Stop-FixtureChild $agentOne.process }
        if ($null -ne $agentTwo) { Stop-FixtureChild $agentTwo.process }
        if ($null -ne $agentThree) { Stop-FixtureChild $agentThree.process }
        if ($null -ne $api) { Stop-FixtureChild $api.process }
    }

    foreach ($role in @('both','monitor','collector')) {
        $case = New-InstallerFixture $role
        $extra = @()
        if ($role -eq 'collector') {
            $inputDirectory = Join-Path $case.root 'private-input'
            $null = & $case.payload setup inbox --directory $inputDirectory --json
            if ($LASTEXITCODE -ne 0) { throw '创建夹具私有输入失败。' }
            $pairInput = Join-Path $inputDirectory 'pair.json'
            [IO.File]::WriteAllText($pairInput, '{"code":"FAKE_INSTALLER_PAIR_CODE","fingerprint":"0123456789abcdef"}', [Text.UTF8Encoding]::new($false))
            $extra = @('/LGMONMONITOR=fake-monitor:7443',('/LGMONPAIRINPUT=' + $pairInput))
        }
        if ($role -eq 'both') {
            $case.home = Join-Path $case.root 'home space 中文 💾'
            $extra = @('/LGMONHOME=' + $case.home)
        }
        Invoke-FixtureInstall $case $role $true $extra
        $state = Read-FixtureState $case
        if (-not $state.installed -or $state.state -ne 'running' -or $state.definition.Role -ne $role -or $state.registered -ne 1 -or $state.started -ne 1) { throw ('三角色安装状态不符：' + $role) }
        $receipt = Join-Path $case.home 'data\setup\installation.json'
        $record = Get-Content -LiteralPath $receipt -Raw | ConvertFrom-Json
        if ($record.role -ne $role -or $record.installedAt -le 0 -or $record.version -ne 'installer-fixture') { throw '安装回执不符。' }
        $metadata = [IO.File]::ReadAllBytes((Join-Path $case.root 'program\setup.ini'))
        if ($metadata.Length -lt 2 -or $metadata[0] -ne 255 -or $metadata[1] -ne 254 -or -not ([Text.Encoding]::Unicode.GetString($metadata).Contains('Home='+$case.home))) { throw '安装归属文件没有无损保存 UTF-16 数据目录。' }
        if ($role -eq 'collector' -and ($state.paired -ne 1 -or (Test-Path -LiteralPath $pairInput))) { throw '配对未完成或私有输入未清理。' }

        # A changed payload sentinel proves that the second installer did not overwrite it.
        $append = $null
        for ($attempt=0; $attempt -lt 50; $attempt++) {
            try { $append = [IO.File]::Open($case.program, [IO.FileMode]::Append, [IO.FileAccess]::Write); break }
            catch [IO.IOException] {
                if (($_.Exception.GetBaseException().HResult -band 65535) -notin @(32,33)) { throw }
                Start-Sleep -Milliseconds 100
            }
        }
        if ($null -eq $append) { throw '自有夹具载荷在进程退出后仍被共享锁占用，未修改程序或其他进程。' }
        try { $bytes=[Text.Encoding]::ASCII.GetBytes('FAKE_INSTALLED_PAYLOAD_SENTINEL'); $append.Write($bytes,0,$bytes.Length) }
        finally { $append.Dispose() }
        $beforeProgram = (Get-FileHash -LiteralPath $case.program -Algorithm SHA256).Hash
        Invoke-FixtureInstall $case $role $false $extra
        if ((Get-FileHash -LiteralPath $case.program -Algorithm SHA256).Hash -ne $beforeProgram -or (Read-FixtureState $case).registered -ne 1) { throw '重复安装覆盖了现有程序或重新注册服务。' }

        $preserved = Join-Path $case.home 'data\kept-user-file.txt'
        [IO.File]::WriteAllText($preserved,'FAKE_USER_DATA_KEEP',[Text.UTF8Encoding]::new($false))
        $homeFiles = @(Get-ChildItem -LiteralPath $case.home -Recurse -File | ForEach-Object { [pscustomobject]@{path=$_.FullName;hash=(Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash} })
        Invoke-FixtureUninstall $case $true
        if ((Read-FixtureState $case).installed -or (Test-Path -LiteralPath $case.program)) { throw '卸载没有移除本安装服务或程序。' }
        foreach ($file in $homeFiles) {
            if (-not (Test-Path -LiteralPath $file.path -PathType Leaf) -or (Get-FileHash -LiteralPath $file.path -Algorithm SHA256).Hash -ne $file.hash) { throw ('卸载改变了 home：' + $file.path) }
        }
        $results += [pscustomobject]@{case=$role;installed=$true;metadataUtf16=$true;duplicateRejected=$true;uninstalled=$true;homePreserved=$true;realSCM=$false}
    }

    # An update replaces the program in place: the old service goes first, the data stays, the new one starts.
    $upgrade = New-InstallerFixture 'upgrade'
    Invoke-FixtureInstall $upgrade 'both' $true
    # Every file the first install made stays byte for byte; only the install receipt is rewritten.
    $receiptPath = Join-Path $upgrade.home 'data\setup\installation.json'
    $before = @(Get-ChildItem -LiteralPath $upgrade.home -Recurse -File | Where-Object { $_.FullName -ne $receiptPath } | ForEach-Object { [pscustomobject]@{path=$_.FullName;hash=(Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash} })
    Invoke-FixtureInstall $upgrade 'both' $true @('/LGMONUPGRADE')
    $state = Read-FixtureState $upgrade
    if (-not $state.installed -or $state.state -ne 'running' -or $state.registered -ne 2 -or $state.removed -ne 1) { throw '原位更新没有先移除旧服务再重新注册。' }
    foreach ($file in $before) {
        if (-not (Test-Path -LiteralPath $file.path -PathType Leaf) -or (Get-FileHash -LiteralPath $file.path -Algorithm SHA256).Hash -ne $file.hash) { throw ('原位更新改变了 home：' + $file.path) }
    }
    if (-not (Select-String -LiteralPath $upgrade.log -SimpleMatch 'lgmon-stage: upgrade' -Quiet) -or -not (Select-String -LiteralPath $upgrade.log -SimpleMatch 'lgmon-stage: service' -Quiet)) { throw '安装日志缺少进度阶段标记。' }
    # lgmon-setup, the installer people see, drives the same engine silently for both a fresh install and an update.
    $front = New-InstallerFixture 'front-end'
    $frontProgram = Join-Path $front.root 'lgmon-setup.exe'
    & $script:goExecutable build -trimpath -o $frontProgram './cmd/lgmon-setup'
    if ($LASTEXITCODE -ne 0) { throw 'lgmon-setup 构建失败。' }
    $null = Invoke-FixtureProcess $frontProgram @('--silent','--engine',$front.installer,'--role','both','--home',$front.home) $true
    if (-not (Read-FixtureState $front).installed) { throw 'lgmon-setup 静默安装没有完成。' }
    $null = Invoke-FixtureProcess $frontProgram @('--silent','--update','--engine',$front.installer,'--role','both','--home',$front.home) $true
    $state = Read-FixtureState $front
    if (-not $state.installed -or $state.registered -ne 2 -or $state.removed -ne 1) { throw 'lgmon-setup 更新没有按原角色和目录重装。' }
    Invoke-FixtureUninstall $upgrade $true
    Invoke-FixtureUninstall $front $true
    $results += [pscustomobject]@{case='upgrade';oldServiceRemovedFirst=$true;homePreserved=$true;frontEndSilent=$true;realSCM=$false}

    $failure = New-InstallerFixture 'startup-failure'
    [IO.File]::WriteAllText((Join-Path $failure.root 'fault.txt'),'start')
    Invoke-FixtureInstall $failure 'both' $false
    $state = Read-FixtureState $failure
    if ($state.installed -or $state.removed -ne 1 -or (Test-Path -LiteralPath (Join-Path $failure.home 'data\setup\installation.json'))) { throw '启动失败没有回滚或写入了成功回执。' }
    Invoke-FixtureUninstall $failure $true
    $results += [pscustomobject]@{case='startup-failure';rolledBack=$true;realSCM=$false}

    $pairFailure = New-InstallerFixture 'pairing-failure'
    [IO.File]::WriteAllText((Join-Path $pairFailure.root 'fault.txt'),'pair')
    $inputDirectory = Join-Path $pairFailure.root 'private-input'
    $null = & $pairFailure.payload setup inbox --directory $inputDirectory --json
    if ($LASTEXITCODE -ne 0) { throw '创建配对失败用例的输入目录失败。' }
    $pairInput = Join-Path $inputDirectory 'pair.json'
    [IO.File]::WriteAllText($pairInput,'{"code":"FAKE_INSTALLER_PAIR_CODE","fingerprint":"0123456789abcdef"}',[Text.UTF8Encoding]::new($false))
    Invoke-FixtureInstall $pairFailure 'collector' $false @('/LGMONMONITOR=fake-monitor:7443',('/LGMONPAIRINPUT='+$pairInput))
    if (Test-Path -LiteralPath (Join-Path $pairFailure.root 'scm.json')) { throw '配对失败仍注册了服务。' }
    Invoke-FixtureUninstall $pairFailure $true
    $results += [pscustomobject]@{case='pairing-failure';noServiceRegistered=$true;realSCM=$false}

    $receiptFailure = New-InstallerFixture 'receipt-failure'
    [IO.File]::WriteAllText((Join-Path $receiptFailure.root 'fault.txt'),'receipt')
    Invoke-FixtureInstall $receiptFailure 'both' $false
    $state = Read-FixtureState $receiptFailure
    if ($state.installed -or $state.removed -ne 1 -or (Test-Path -LiteralPath (Join-Path $receiptFailure.home 'data\setup\installation.json'))) { throw '回执写失败仍声称成功或没有回滚。' }
    Invoke-FixtureUninstall $receiptFailure $true
    $results += [pscustomobject]@{case='receipt-failure';rolledBack=$true;realSCM=$false}

    $binding = New-InstallerFixture 'uninstall-guard'
    Invoke-FixtureInstall $binding 'both' $true
    [IO.File]::WriteAllText((Join-Path $binding.root 'fault.txt'),'binding')
    Invoke-FixtureUninstall $binding $false
    if (-not (Test-Path -LiteralPath $binding.program) -or -not (Read-FixtureState $binding).installed -or (Read-FixtureState $binding).removed -ne 0) { throw '绑定不匹配仍停止服务或删除程序。' }
    [IO.File]::WriteAllText((Join-Path $binding.root 'fault.txt'),'remove')
    Invoke-FixtureUninstall $binding $false
    if (-not (Test-Path -LiteralPath $binding.program) -or -not (Read-FixtureState $binding).installed) { throw '服务移除失败仍删除程序。' }
    Remove-Item -LiteralPath (Join-Path $binding.root 'fault.txt')
    Invoke-FixtureUninstall $binding $true
    $results += [pscustomobject]@{case='uninstall-guard';mismatchRejected=$true;removeFailureKeptProgram=$true;realSCM=$false}

    $confirmation = New-InstallerFixture 'confirmation'
    $null = Invoke-FixtureProcess $confirmation.installer @('/VERYSILENT','/SUPPRESSMSGBOXES','/NORESTART','/SP-','/LGMONROLE=both') $false
    if (Test-Path -LiteralPath $confirmation.program) { throw '缺少确认仍复制了程序。' }
    $outside = Join-Path $fixtureRoot 'outside-the-bound-case'
    $null = Invoke-FixtureProcess $confirmation.payload @('setup','install','--home',$outside,'--confirm','--json') $false
    if (Test-Path -LiteralPath $outside) { throw '夹具越出编译绑定目录。' }
    $null = Invoke-FixtureProcess $confirmation.payload @('install','--role','both') $false
    $results += [pscustomobject]@{case='confirmation';explicitConsentRequired=$true;scopeBound=$true;noNativeCommands=$true;realSCM=$false}

    foreach ($log in Get-ChildItem -LiteralPath $fixtureRoot -Recurse -File -Filter '*.log') {
        if ([IO.File]::ReadAllText($log.FullName).Contains('FAKE_INSTALLER_PAIR_CODE')) { throw ('配对码泄漏进安装日志：'+$log.FullName) }
    }
    $results += [pscustomobject]@{case='logs';pairingCodeAbsent=$true;realSCM=$false}

    if ($EvidenceDirectory) {
        $evidence = [IO.Path]::GetFullPath($EvidenceDirectory)
        New-Item -ItemType Directory -Path $evidence -Force | Out-Null
        [IO.File]::WriteAllText((Join-Path $evidence 'm2-09-installer-results.json'), ($results | ConvertTo-Json -Depth 5), [Text.UTF8Encoding]::new($false))
    }
    $results | ConvertTo-Json -Depth 5
    Write-Output ('安装夹具验证完成，日志保留于：' + $fixtureRoot)
}
finally {
    $env:CGO_ENABLED = $previousCGO
    Set-Location -LiteralPath $previousDirectory.Path
}
