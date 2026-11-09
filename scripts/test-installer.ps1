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
    if (($process.ExitCode -eq 0) -ne $Success) { throw ('安装夹具退出码不符：' + $process.ExitCode + '；' + $Executable) }
    return $process.ExitCode
}

function New-InstallerFixture([string]$Name) {
    $root = Join-Path $fixtureRoot $Name
    New-Item -ItemType Directory -Path (Join-Path $root 'payload') | Out-Null
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

try {
    Set-Location -LiteralPath $projectRoot
    $env:CGO_ENABLED = '0'
    $go = Get-Command go -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($go) { $script:goExecutable = $go.Source }
    else { $script:goExecutable = Join-Path $env:ProgramFiles 'Go\bin\go.exe' }
    $CompilerPath = (Resolve-Path -LiteralPath $CompilerPath).ProviderPath

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
            $case.home = Join-Path $case.root 'home space 中文'
            $extra = @('/LGMONHOME=' + $case.home)
        }
        Invoke-FixtureInstall $case $role $true $extra
        $state = Read-FixtureState $case
        if (-not $state.installed -or $state.state -ne 'running' -or $state.definition.Role -ne $role -or $state.registered -ne 1 -or $state.started -ne 1) { throw ('三角色安装状态不符：' + $role) }
        $receipt = Join-Path $case.home 'data\setup\installation.json'
        $record = Get-Content -LiteralPath $receipt -Raw | ConvertFrom-Json
        if ($record.role -ne $role -or $record.installedAt -le 0 -or $record.version -ne 'installer-fixture') { throw '安装回执不符。' }
        if ($role -eq 'collector' -and ($state.paired -ne 1 -or (Test-Path -LiteralPath $pairInput))) { throw '配对未完成或私有输入未清理。' }

        # A changed payload sentinel proves that the second installer did not overwrite it.
        $append = [IO.File]::Open($case.program, [IO.FileMode]::Append, [IO.FileAccess]::Write)
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
        $results += [pscustomobject]@{case=$role;installed=$true;duplicateRejected=$true;uninstalled=$true;homePreserved=$true;realSCM=$false}
    }

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
