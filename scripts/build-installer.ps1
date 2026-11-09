[CmdletBinding()]
param(
    [ValidatePattern('^[0-9A-Za-z][0-9A-Za-z.+_-]*$')]
    [string]$Version = 'dev',
    [string]$CompilerPath,
    [switch]$SkipBuild
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$previousDirectory = Get-Location
try {
    Set-Location -LiteralPath $projectRoot
    if (-not $CompilerPath) {
        $command = Get-Command ISCC.exe -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
        if ($command) { $CompilerPath = $command.Source }
        else { $CompilerPath = Join-Path ${env:ProgramFiles(x86)} 'Inno Setup 6\ISCC.exe' }
    }
    if (-not (Test-Path -LiteralPath $CompilerPath -PathType Leaf)) {
        throw '未找到 Inno Setup 6.7.4。请传入 -CompilerPath，或先使用 scripts/get-inno.ps1 获取官方编译工具。'
    }
    $CompilerPath = (Resolve-Path -LiteralPath $CompilerPath).ProviderPath
    if (-not $SkipBuild) {
        & (Join-Path $PSScriptRoot 'build.ps1') -Version $Version
        if ($LASTEXITCODE -ne 0) { throw '程序构建失败，没有打包。' }
    }
    $program = Join-Path $projectRoot 'bin\lgmon.exe'
    if (-not (Test-Path -LiteralPath $program -PathType Leaf)) { throw '缺少 bin/lgmon.exe，请先构建。' }
    $goCommand = Get-Command go -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($goCommand) { $goExecutable = $goCommand.Source }
    else { $goExecutable = Join-Path $env:ProgramFiles 'Go\bin\go.exe' }
    $buildInfo = @(& $goExecutable version -m $program)
    if ($LASTEXITCODE -ne 0) { throw '无法读取程序的 Go 构建信息。' }
    $details = $buildInfo -join "`n"
    if ($details -match 'installerfixture' -or $details -notmatch 'CGO_ENABLED=0' -or $details -notmatch 'GOOS=windows' -or $details -notmatch 'GOARCH=amd64' -or $details -notmatch '-tags=webui') {
        throw '只允许打包嵌入网页的 Windows amd64、CGO_ENABLED=0 正式载荷；禁止夹具或其他架构。'
    }
    $reportedVersion = @(& $program version)
    if ($LASTEXITCODE -ne 0 -or $reportedVersion.Count -eq 0 -or $reportedVersion[0] -ne ('版本：' + $Version)) { throw '载荷版本与 -Version 不匹配，请重新构建。' }
    $null = & $program setup --help
    if ($LASTEXITCODE -ne 0) { throw '载荷没有 setup 安装编排命令，不能生成安装包。' }

    $reader = [IO.BinaryReader]::new([IO.File]::OpenRead($program))
    try {
        if ($reader.ReadUInt16() -ne 0x5A4D) { throw '载荷不是 Windows PE 程序。' }
        $reader.BaseStream.Position = 0x3C
        $header = $reader.ReadUInt32()
        $reader.BaseStream.Position = $header
        if ($reader.ReadUInt32() -ne 0x4550 -or $reader.ReadUInt16() -ne 0x8664) { throw '载荷不是 x64 PE 程序。' }
    }
    finally { $reader.Dispose() }

    $numeric = '0.0.0.0'
    if ($Version -match '^v?(\d+)\.(\d+)\.(\d+)(?:\.(\d+))?(?:[-+_].*)?$') {
        $parts = @($Matches[1], $Matches[2], $Matches[3], '0')
        if ($Matches.ContainsKey(4) -and $Matches[4]) { $parts[3] = $Matches[4] }
        foreach ($part in $parts) { if ([long]$part -gt 65535) { throw 'Windows 版本号的每一段必须为 0～65535。' } }
        $numeric = $parts -join '.'
    }
    $output = Join-Path $projectRoot 'dist'
    New-Item -ItemType Directory -Path $output -Force | Out-Null
    & $CompilerPath '/Q' ('/DAppVersion=' + $Version) ('/DNumericVersion=' + $numeric) ('/DPayloadFile=' + $program) ('/DOutputDirectory=' + $output) (Join-Path $PSScriptRoot 'lgmon.iss')
    if ($LASTEXITCODE -ne 0) { throw 'Inno Setup 编译失败，请检查编译器版本和上面的错误。' }
    $installer = Join-Path $output ('lgmon-' + $Version + '-windows-amd64-setup.exe')
    if (-not (Test-Path -LiteralPath $installer -PathType Leaf)) { throw '编译器没有生成预期安装包。' }
    $head = & git rev-parse HEAD
    if ($LASTEXITCODE -ne 0) { throw '无法记录安装包源代码版本。' }
    $dirty = [bool](@(git status --porcelain).Count)
    $manifest = [ordered]@{
        product = 'lg-monitor'; version = $Version; commit = $head.Trim(); workingTreeDirty = $dirty
        platform = 'windows/amd64'; cgoEnabled = $false; frontendEmbedded = $true; fixture = $false
        builtAt = [DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ')
        compiler = [ordered]@{name='Inno Setup'; sha256=(Get-FileHash -LiteralPath $CompilerPath -Algorithm SHA256).Hash.ToLowerInvariant()}
        program = [ordered]@{file='lgmon.exe'; sha256=(Get-FileHash -LiteralPath $program -Algorithm SHA256).Hash.ToLowerInvariant(); reportedVersion=($reportedVersion -join ' ')}
        installer = [ordered]@{file=[IO.Path]::GetFileName($installer); sha256=(Get-FileHash -LiteralPath $installer -Algorithm SHA256).Hash.ToLowerInvariant(); authenticode=(Get-AuthenticodeSignature -LiteralPath $installer).Status.ToString()}
        language = [ordered]@{source='https://github.com/jrsoftware/issrc/blob/is-6_7_4/Files/Languages/Unofficial/ChineseSimplified.isl';sha256=(Get-FileHash -LiteralPath (Join-Path $PSScriptRoot 'ChineseSimplified.isl') -Algorithm SHA256).Hash.ToLowerInvariant()}
    }
    $manifestPath = Join-Path $output ('lgmon-' + $Version + '-windows-amd64-setup.json')
    [IO.File]::WriteAllText($manifestPath, ($manifest | ConvertTo-Json -Depth 6), [Text.UTF8Encoding]::new($false))
    Write-Output ('已生成安装包：' + $installer)
    Write-Output ('已生成构建记录：' + $manifestPath)
}
finally { Set-Location -LiteralPath $previousDirectory.Path }
