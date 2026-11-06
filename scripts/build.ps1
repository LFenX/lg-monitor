[CmdletBinding()]
param(
    [ValidatePattern('^[0-9A-Za-z][0-9A-Za-z.+_-]*$')]
    [string]$Version = 'dev'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$projectRoot = Split-Path -Parent $PSScriptRoot
$previousCGO = $env:CGO_ENABLED
$previousGOOS = $env:GOOS
$previousGOARCH = $env:GOARCH

Push-Location $projectRoot
try {
    $goCommand = Get-Command go -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if (-not $goCommand) {
        $installedGo = Join-Path $env:ProgramFiles 'Go\bin\go.exe'
        if (-not (Test-Path -LiteralPath $installedGo -PathType Leaf)) {
            throw 'Go 未安装或不在 PATH 中，请安装 Go 1.27 后重新运行。'
        }
        $goExecutable = $installedGo
    }
    else {
        $goExecutable = $goCommand.Source
    }

    $commit = & git rev-parse --short HEAD
    if ($LASTEXITCODE -ne 0) {
        throw '读取 Git 提交号失败，请在完整的 Git 仓库中构建。'
    }
    $buildTime = [DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ')
    $linkerFlags = '-X main.version={0} -X main.commit={1} -X main.buildTime={2}' -f $Version, $commit.Trim(), $buildTime

    New-Item -ItemType Directory -Path (Join-Path $projectRoot 'bin') -Force | Out-Null
    $env:CGO_ENABLED = '0'
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    & $goExecutable build -trimpath -ldflags $linkerFlags -o 'bin\lgmon.exe' './cmd/lgmon'
    if ($LASTEXITCODE -ne 0) {
        throw '构建失败，请修复上面的 Go 编译错误后重试。'
    }
    Write-Output ('已生成：{0}' -f (Join-Path $projectRoot 'bin\lgmon.exe'))
}
finally {
    $env:CGO_ENABLED = $previousCGO
    $env:GOOS = $previousGOOS
    $env:GOARCH = $previousGOARCH
    Pop-Location
}
