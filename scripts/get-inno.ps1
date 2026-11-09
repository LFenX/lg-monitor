[CmdletBinding()]
param(
    [string]$Directory,
    [switch]$Remove
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$pin = '6.7.4'
$expected = 'a549929319f9120716677c685535ca4671e4b937b5fdc87e2585f32d8eb30e8c'
$temporaryRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\') + '\'
if (-not $Directory) { $Directory = Join-Path $temporaryRoot ('lgmon-inno-' + [Guid]::NewGuid().ToString('N')) }
$Directory = [IO.Path]::GetFullPath($Directory).TrimEnd('\')
if (-not $Directory.StartsWith($temporaryRoot, [StringComparison]::OrdinalIgnoreCase) -or $Directory -eq $temporaryRoot.TrimEnd('\')) { throw '编译工具的临时目录必须位于系统临时目录内部。' }
$compilerDirectory = Join-Path $Directory 'compiler'
$ownership = Join-Path $Directory 'tool-owner.json'
$registryPath = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\Inno Setup 6_is1'

if ($Remove) {
    if (-not (Test-Path -LiteralPath $ownership -PathType Leaf)) { throw '没有本脚本创建的工具记录，不执行卸载。' }
    $record = Get-Content -LiteralPath $ownership -Raw | ConvertFrom-Json
    if ($record.directory -ne $Directory -or $record.compilerDirectory -ne $compilerDirectory -or $record.setupSha256 -ne $expected -or -not $record.owned) { throw '临时工具记录不匹配，不执行卸载。' }
    if (Test-Path -LiteralPath $registryPath) {
        $registered = Get-ItemProperty -LiteralPath $registryPath
        if ([IO.Path]::GetFullPath($registered.InstallLocation).TrimEnd('\') -ne $compilerDirectory) { throw '编译器登记已指向其他安装，拒绝移除。' }
    }
    $uninstaller = Join-Path $compilerDirectory 'unins000.exe'
    if (Test-Path -LiteralPath $uninstaller -PathType Leaf) {
        $process = Start-Process -FilePath $uninstaller -ArgumentList @('/VERYSILENT','/SUPPRESSMSGBOXES','/NORESTART',('/LOG="' + (Join-Path $Directory 'tool-uninstall.log') + '"')) -WindowStyle Hidden -Wait -PassThru
        if ($process.ExitCode -ne 0) { throw ('临时编译工具卸载失败：' + $process.ExitCode) }
    }
    Write-Output ('已回收临时 Inno Setup 编译工具：' + $compilerDirectory)
    return
}

if (Test-Path -LiteralPath $registryPath) {
    $registered = Get-ItemProperty -LiteralPath $registryPath
    $existing = Join-Path $registered.InstallLocation 'ISCC.exe'
    if ([Version]$registered.DisplayVersion -ge [Version]$pin -and [Version]$registered.DisplayVersion -lt [Version]'7.0' -and (Test-Path -LiteralPath $existing -PathType Leaf)) {
        [pscustomobject]@{compiler=$existing; version=$registered.DisplayVersion; owned=$false; directory=$null} | ConvertTo-Json -Compress
        return
    }
    throw '当前账户已有其他 Inno Setup 6，本脚本不会覆盖；请使用已有兼容编译器或本人先升级。'
}
if (Test-Path -LiteralPath $Directory) { throw '临时工具目录已存在，拒绝接管。' }
New-Item -ItemType Directory -Path $Directory | Out-Null
$setup = Join-Path $Directory ('innosetup-' + $pin + '.exe')
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
Invoke-WebRequest -UseBasicParsing -Uri ('https://github.com/jrsoftware/issrc/releases/download/is-6_7_4/innosetup-' + $pin + '.exe') -OutFile $setup
if ((Get-FileHash -LiteralPath $setup -Algorithm SHA256).Hash.ToLowerInvariant() -ne $expected) { throw '官方 Inno Setup 下载 SHA-256 不匹配。' }
$signature = Get-AuthenticodeSignature -LiteralPath $setup
if ($signature.Status -ne 'Valid' -or $signature.SignerCertificate.GetNameInfo([Security.Cryptography.X509Certificates.X509NameType]::SimpleName, $false) -ne 'Pyrsys B.V.') { throw '官方 Inno Setup 安装器的签名验证失败。' }
$arguments = @('/VERYSILENT','/SUPPRESSMSGBOXES','/NORESTART','/SP-','/CURRENTUSER','/PORTABLE=0','/TASKS=','/NOICONS',('/DIR="' + $compilerDirectory + '"'),('/LOG="' + (Join-Path $Directory 'tool-install.log') + '"'))
$process = Start-Process -FilePath $setup -ArgumentList $arguments -WindowStyle Hidden -Wait -PassThru
if ($process.ExitCode -ne 0) { throw ('官方编译工具安装失败：' + $process.ExitCode) }
$compiler = Join-Path $compilerDirectory 'ISCC.exe'
if (-not (Test-Path -LiteralPath $compiler -PathType Leaf)) { throw '标准安装器没有生成 ISCC.exe。' }
$record = [ordered]@{directory=$Directory;compilerDirectory=$compilerDirectory;setupSha256=$expected;owned=$true}
[IO.File]::WriteAllText($ownership, ($record | ConvertTo-Json), [Text.UTF8Encoding]::new($false))
[pscustomobject]@{compiler=$compiler;version=$pin;owned=$true;directory=$Directory} | ConvertTo-Json -Compress
