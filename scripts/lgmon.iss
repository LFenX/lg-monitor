#ifndef AppVersion
  #define AppVersion "dev"
#endif
#ifndef NumericVersion
  #define NumericVersion "0.0.0.0"
#endif
#ifndef PayloadFile
  #define PayloadFile "..\bin\lgmon.exe"
#endif
#ifndef OutputDirectory
  #define OutputDirectory "..\dist"
#endif
#ifdef FixtureMode
  #ifndef FixtureRoot
    #error FixtureRoot is required for an isolated fixture build
  #endif
  #ifndef FixtureTag
    #error FixtureTag is required for an isolated fixture build
  #endif
  #define ProductName "lg-monitor 工程测试夹具"
#else
  #define ProductName "lg-monitor"
#endif

[Setup]
#ifdef FixtureMode
AppId=lg-monitor-fixture-{#FixtureTag}
DefaultDirName={#FixtureRoot}\program
OutputBaseFilename=lgmon-installer-fixture-{#FixtureTag}
PrivilegesRequired=lowest
CreateUninstallRegKey=no
SetupMutex=Local\lg-monitor-installer-fixture-{#FixtureTag}
Compression=zip
#else
AppId={{39B64C4F-BFE7-46A2-9201-217A293487D1}
DefaultDirName={autopf64}\lg-monitor
OutputBaseFilename=lgmon-{#AppVersion}-windows-amd64-setup
PrivilegesRequired=admin
SetupMutex=Global\lg-monitor-installer-package
Compression=lzma2/normal
#endif
AppName={#ProductName}
AppVersion={#AppVersion}
AppVerName={#ProductName} {#AppVersion}
AppPublisher=lg-monitor
AppPublisherURL=https://github.com/LFenX/lg-monitor
AppSupportURL=https://github.com/LFenX/lg-monitor/issues
VersionInfoVersion={#NumericVersion}
OutputDir={#OutputDirectory}
ArchitecturesAllowed=x64os
ArchitecturesInstallIn64BitMode=x64os
MinVersion=10.0
WizardStyle=modern
; build-installer.ps1 draws these from lgmon's mark (scripts/installer-art.ps1); fixture builds keep the defaults.
#ifdef ArtIcon
WizardImageFile={#WizardImages}
WizardSmallImageFile={#WizardSmallImages}
SetupIconFile={#ArtIcon}
#endif
DisableWelcomePage=no
DisableDirPage=yes
DisableProgramGroupPage=yes
UsePreviousAppDir=no
UsePreviousTasks=no
CloseApplications=no
RestartApplications=no
RestartIfNeededByRun=no
AlwaysRestart=no
#ifdef ArtIcon
UninstallDisplayIcon={app}\lgmon.ico
#else
UninstallDisplayIcon={app}\lgmon.exe
#endif
UninstallDisplayName={#ProductName}
UninstallLogMode=overwrite
SetupLogging=yes
UninstallLogging=yes

[Languages]
Name: "chinesesimp"; MessagesFile: "ChineseSimplified.isl"

[Messages]
WelcomeLabel2=lgmon 监控这台电脑上的服务：网站、端口、Windows 服务、进程和计划任务，出问题时第一时间提醒你。%n%n接下来选择这台电脑的角色和数据目录。安装会注册并启动 lgmon 服务，不会关闭其他程序、修改防火墙或重启电脑；卸载时保留配置和数据。
FinishedLabel=lgmon 已经在后台运行。%n%n打开 lgmon 后，在小窗口里点“复制这段话”发给你的 Agent（Claude Code、Codex 等），它会问你想监控什么，然后帮你加好。
FinishedLabelNoIcons=lgmon 已经在后台运行。%n%n打开 lgmon 后，在小窗口里点“复制这段话”发给你的 Agent（Claude Code、Codex 等），它会问你想监控什么，然后帮你加好。

[Files]
Source: "{#PayloadFile}"; DestDir: "{app}"; DestName: "lgmon.exe"; Flags: ignoreversion
#ifdef WindowFile
Source: "{#WindowFile}"; DestDir: "{app}"; DestName: "lgmon-window.exe"; Flags: ignoreversion
#endif
#ifdef ArtIcon
Source: "{#ArtIcon}"; DestDir: "{app}"; DestName: "lgmon.ico"; Flags: ignoreversion
#endif

#ifndef FixtureMode
; lgmon.exe without a command opens lgmon's small window: status, the agent card and the board.
[Icons]
#ifdef ArtIcon
Name: "{autoprograms}\lgmon"; Filename: "{app}\lgmon.exe"; Parameters: "--home ""{code:DataHome}"""; WorkingDir: "{app}"; Comment: "lgmon 的服务状况和接入 Agent"; IconFilename: "{app}\lgmon.ico"; Check: HasMonitor
#else
Name: "{autoprograms}\lgmon"; Filename: "{app}\lgmon.exe"; Parameters: "--home ""{code:DataHome}"""; WorkingDir: "{app}"; Comment: "lgmon 的服务状况和接入 Agent"; Check: HasMonitor
#endif
Name: "{autoprograms}\lgmon 看板"; Filename: "{code:PanelAddress}"; Check: HasMonitor

[Run]
Filename: "{app}\lgmon.exe"; Parameters: "--home ""{code:DataHome}"""; Description: "打开 lgmon"; Flags: nowait postinstall skipifsilent runhidden runasoriginaluser; Check: HasMonitor
Filename: "{code:PanelAddress}"; Description: "在浏览器里打开看板"; Flags: nowait postinstall skipifsilent shellexec runasoriginaluser unchecked; Check: HasMonitor
#endif

[UninstallDelete]
Type: files; Name: "{app}\setup.ini"
Type: files; Name: "{app}\lgmon.exe.previous-*"
Type: files; Name: "{app}\lgmon-window.exe.previous-*"

[Code]
type
  TSetupSecurityAttributes = record
    Length: LongWord;
    Descriptor: LongWord;
    Inherit: LongWord;
  end;

var
  RolePage: TInputOptionWizardPage;
  HomePage: TInputDirWizardPage;
  ListenPage, PairPage: TInputQueryWizardPage;
  ReusePair: TNewCheckBox;
  GuardHandle: THandle;
  GuardHeld, SetupSucceeded: Boolean;
  PairInput, CachedPanel, UninstallHome: String;
  PreviousProgram, PreviousWindow: String;
  PurgeData: Boolean;

function NativeCreateMutex(Attributes: LongWord; InitialOwner: Boolean; Name: String): THandle;
  external 'CreateMutexW@kernel32.dll stdcall';
function NativeWait(Handle: THandle; Milliseconds: LongWord): LongWord;
  external 'WaitForSingleObject@kernel32.dll stdcall';
function NativeReleaseMutex(Handle: THandle): Boolean;
  external 'ReleaseMutex@kernel32.dll stdcall';
function NativeCloseHandle(Handle: THandle): Boolean;
  external 'CloseHandle@kernel32.dll stdcall';
function NativeMoveFile(Existing, Replacement: String; Flags: LongWord): Boolean;
  external 'MoveFileExW@kernel32.dll stdcall';
function NativeLastError: LongWord;
  external 'GetLastError@kernel32.dll stdcall';
function NativeDeleteFile(FileName: String): Boolean;
  external 'DeleteFileW@kernel32.dll stdcall';
function NativeCreateFile(FileName: String; Access, Share, Attributes, Disposition, Flags: LongWord; Template: THandle): THandle;
  external 'CreateFileW@kernel32.dll stdcall';
function NativeWriteFile(Handle: THandle; Buffer: String; Count: LongWord; var Written: LongWord; Overlapped: LongWord): Boolean;
  external 'WriteFile@kernel32.dll stdcall';
function NativeSecurityDescriptor(Value: String; Revision: LongWord; var Descriptor: LongWord; Size: LongWord): Boolean;
  external 'ConvertStringSecurityDescriptorToSecurityDescriptorW@advapi32.dll stdcall';
function NativeCreateDirectory(Path: String; var Attributes: TSetupSecurityAttributes): Boolean;
  external 'CreateDirectoryW@kernel32.dll stdcall';
function NativeLocalFree(Value: LongWord): LongWord;
  external 'LocalFree@kernel32.dll stdcall';

function GuardName: String;
begin
#ifdef FixtureMode
  Result := 'Local\lg-monitor-setup-fixture-{#FixtureTag}';
#else
  Result := 'Global\lg-monitor-setup';
#endif
end;

procedure AcquireGuard;
var
  State: LongWord;
begin
  if GuardHeld then Exit;
  if GuardHandle = 0 then GuardHandle := NativeCreateMutex(0, False, GuardName);
  if GuardHandle = 0 then RaiseException('无法取得 lg-monitor 安装互斥，请关闭其他安装/卸载窗口后重试。');
  State := NativeWait(GuardHandle, 0);
  if (State <> 0) and (State <> $80) then
    RaiseException('另一个 lg-monitor 安装或卸载正在进行。请等待它结束。');
  GuardHeld := True;
end;

procedure ReleaseGuard;
begin
  if GuardHeld then begin
    NativeReleaseMutex(GuardHandle);
    GuardHeld := False;
  end;
end;

procedure CloseGuard;
begin
  ReleaseGuard;
  if GuardHandle <> 0 then begin
    NativeCloseHandle(GuardHandle);
    GuardHandle := 0;
  end;
end;

function Quoted(Value: String): String;
begin
  if (Pos('"', Value) <> 0) or (Pos(#13, Value) <> 0) or (Pos(#10, Value) <> 0) then
    RaiseException('目录和地址不能含双引号或换行。');
  Result := '"' + Value + '"';
end;

function JsonString(Value: String): String;
var
  I, C: Integer;
begin
  Result := '"';
  for I := 1 to Length(Value) do begin
    C := Ord(Value[I]);
    if Value[I] = '"' then Result := Result + '\"'
    else if Value[I] = '\' then Result := Result + '\\'
    else if C < 32 then Result := Result + '\u' + Format('%.4x', [C])
    else Result := Result + Value[I];
  end;
  Result := Result + '"';
end;

function HasSwitch(Name: String): Boolean;
var
  I: Integer;
begin
  Result := False;
  for I := 1 to ParamCount do
    if CompareText(ParamStr(I), Name) = 0 then Result := True;
end;

function SelectedRole(Param: String): String;
begin
  if RolePage.SelectedValueIndex = 1 then Result := 'monitor'
  else if RolePage.SelectedValueIndex = 2 then Result := 'collector'
  else Result := 'both';
end;

function DataHome(Param: String): String;
begin
  Result := Trim(HomePage.Values[0]);
end;

function HasMonitor: Boolean;
begin
  Result := SelectedRole('') <> 'collector';
end;

function Helper(ProgramFile, Parameters: String; var Text: String): Boolean;
var
  Output: TExecOutput;
  ResultCode, I: Integer;
begin
  Text := '';
  Result := ExecAndCaptureOutput(ProgramFile, Parameters, '', SW_HIDE, ewWaitUntilTerminated, ResultCode, Output);
  Result := Result and (ResultCode = 0) and not Output.Error;
  for I := 0 to GetArrayLength(Output.StdOut) - 1 do
    Text := Text + Output.StdOut[I] + #13#10;
  for I := 0 to GetArrayLength(Output.StdErr) - 1 do
    Text := Text + Output.StdErr[I] + #13#10;
  Text := Trim(Text);
  if not Result and (Text = '') then Text := '安装辅助程序没有确认完成，请保留 home 并检查安装日志。';
end;

function BaseParameters(Command: String): String;
begin
  Result := 'setup ' + Command + ' --home ' + Quoted(DataHome(''));
  if Command <> 'remove' then begin
    Result := Result + ' --role ' + SelectedRole('');
    if HasMonitor then Result := Result + ' --listen ' + Quoted(Trim(ListenPage.Values[0]))
    else Result := Result + ' --monitor ' + Quoted(Trim(PairPage.Values[0]));
  end;
end;

function PanelAddress(Param: String): String;
begin
  Result := CachedPanel;
end;

procedure InitializeWizard;
var
  Role: String;
begin
  RolePage := CreateInputOptionPage(wpWelcome, '选择这台电脑的角色',
    '同一个程序可以采集本机，也可以汇总和告警。',
    '只采集时，需要另一台正在运行的监控器。', True, False);
  RolePage.Add('本机采集与监控（默认）');
  RolePage.Add('只监控与告警');
  RolePage.Add('只采集本机，上报另一台监控器');
  Role := Lowercase(ExpandConstant('{param:LGMONROLE|both}'));
  if Role = 'monitor' then RolePage.SelectedValueIndex := 1
  else if Role = 'collector' then RolePage.SelectedValueIndex := 2
  else if Role = 'both' then RolePage.SelectedValueIndex := 0
  else RaiseException('LGMONROLE 必须为 both、monitor 或 collector。');

  HomePage := CreateInputDirPage(RolePage.ID, '选择数据目录',
    '配置、配对身份、数据库和日志都保存在这里。',
    '已有 lg-monitor 配置会复用；卸载程序会保留此目录。不能选择系统目录或其他应用的数据目录。', False, 'lg-monitor');
  HomePage.Add('数据目录：');
#ifdef FixtureMode
  HomePage.Values[0] := '{#FixtureRoot}\home';
#else
  HomePage.Values[0] := ExpandConstant('{commonappdata}\lg-monitor');
#endif
  HomePage.Values[0] := ExpandConstant('{param:LGMONHOME|' + HomePage.Values[0] + '}');

  ListenPage := CreateInputQueryPage(HomePage.ID, '监控器接收地址',
    '采集器主动连接到这里，传输采用 mTLS。',
    '默认只接收本机采集器。要接收其他电脑，可填写 Tailscale IP:7443 或 0.0.0.0:7443；防火墙放行由本人处理。');
  ListenPage.Add('接收地址（host:port）：', False);
  ListenPage.Values[0] := ExpandConstant('{param:LGMONLISTEN|127.0.0.1:7443}');

  PairPage := CreateInputQueryPage(ListenPage.ID, '连接另一台监控器',
    '先在监控器生成一次性配对码，并通过可信方式核对 CA 指纹。',
    '配对码不会进入进程参数或安装日志。已有此监控器的配对可选择复用。');
  PairPage.Add('监控器地址（host:port）：', False);
  PairPage.Add('一次性配对码：', True);
  PairPage.Add('已核对的 CA 指纹（16 或 64 位）：', False);
  PairPage.Values[0] := ExpandConstant('{param:LGMONMONITOR|}');
  ReusePair := TNewCheckBox.Create(PairPage);
  ReusePair.Parent := PairPage.Surface;
  ReusePair.Left := 0;
  ReusePair.Top := PairPage.Edits[2].Top + PairPage.Edits[2].Height + ScaleY(12);
  ReusePair.Width := PairPage.SurfaceWidth;
  ReusePair.Caption := '复用此数据目录中已有的配对身份（保持监控器地址不变）';
  PairInput := ExpandConstant('{param:LGMONPAIRINPUT|}');
  if WizardSilent and (PairInput = '') then ReusePair.Checked := True;
end;

function ShouldSkipPage(PageID: Integer): Boolean;
begin
  Result := ((PageID = ListenPage.ID) and not HasMonitor) or
    ((PageID = PairPage.ID) and HasMonitor);
end;

function NextButtonClick(CurPageID: Integer): Boolean;
begin
  Result := True;
  if WizardSilent and ((ExpandConstant('{param:LGMONROLE|}') = '') or not HasSwitch('/LGMONCONFIRM')) then begin
    SuppressibleMsgBox('静默安装必须指定 /LGMONROLE=both|monitor|collector 和 /LGMONCONFIRM。', mbError, MB_OK, IDOK);
    Result := False;
    Exit;
  end;
  if (CurPageID = PairPage.ID) and not HasMonitor then begin
    if Trim(PairPage.Values[0]) = '' then begin
      SuppressibleMsgBox('请填写监控器地址。', mbError, MB_OK, IDOK);
      Result := False;
    end else if (PairInput = '') and not ReusePair.Checked and
      ((Trim(PairPage.Values[1]) = '') or ((Length(Trim(PairPage.Values[2])) <> 16) and (Length(Trim(PairPage.Values[2])) <> 64))) then begin
      SuppressibleMsgBox('首次配对需要一次性配对码及本人已核对的 16/64 位 CA 指纹。', mbError, MB_OK, IDOK);
      Result := False;
    end;
  end;
end;

procedure CancelButtonClick(CurPageID: Integer; var Cancel, Confirm: Boolean);
begin
  if (CurPageID <> wpInstalling) and (CurPageID <> wpPreparing) then Confirm := False;
end;

function UpdateReadyMemo(Space, NewLine, MemoUserInfoInfo, MemoDirInfo, MemoTypeInfo, MemoComponentsInfo, MemoGroupInfo, MemoTasksInfo: String): String;
begin
  Result := '角色：' + SelectedRole('') + NewLine +
    '程序目录：' + WizardDirValue + NewLine +
    '数据目录：' + DataHome('') + NewLine + NewLine +
    '点击“安装”即同意注册并启动本产品的 LocalSystem 自动启动服务。' + NewLine +
    '保留已有配置、身份和数据；远程重启默认关闭。' + NewLine +
    '不会修改防火墙、注册登录启动、重启电脑或关闭其他程序。';
  if HasMonitor then Result := Result + NewLine + '接收地址：' + Trim(ListenPage.Values[0])
  else Result := Result + NewLine + '监控器：' + Trim(PairPage.Values[0]);
end;

procedure CleanPreviousImages(Directory, Pattern: String);
var
  Found: TFindRec;
begin
  if FindFirst(Directory + '\' + Pattern, Found) then begin
    try
      repeat
        // Never follow a directory or reparse point, even inside the checked program directory.
        if (Found.Attributes and $410) = 0 then
          if not DeleteFile(Directory + '\' + Found.Name) then
            Log('Previous lgmon image is still in use: ' + Found.Name);
      until not FindNext(Found);
    finally
      FindClose(Found);
    end;
  end;
end;

procedure CleanPreviousPrograms(Directory: String);
begin
  CleanPreviousImages(Directory, 'lgmon.exe.previous-*');
  CleanPreviousImages(Directory, 'lgmon-window.exe.previous-*');
end;

function MoveProgramImage(Existing, Replacement: String): Boolean;
var
  Attempt: Integer;
  ErrorCode: LongWord;
begin
  Result := False;
  for Attempt := 1 to 50 do begin
    if NativeMoveFile(Existing, Replacement, 0) then begin
      Result := True;
      Exit;
    end;
    ErrorCode := NativeLastError;
    if (ErrorCode <> 5) and (ErrorCode <> 32) and (ErrorCode <> 33) then Break;
    // A just-exited helper or a scanner can retain a short-lived handle. Persistent locks still fail before stop.
    Sleep(100);
  end;
  Log('Could not move program image; Windows error: ' + IntToStr(ErrorCode));
end;

function RetireProgram(ProgramFile: String): String;
var
  Index: Integer;
  Prefix: String;
begin
  Prefix := ProgramFile + '.previous-' + GetDateTimeString('yyyymmddhhnnss', '-', ':') + '-';
  Index := 0;
  repeat
    Result := Prefix + IntToStr(Index);
    Index := Index + 1;
  until not FileExists(Result) and not DirExists(Result);
  // A running MCP image cannot be overwritten, but it can keep running under another name on the same volume.
  if not MoveProgramImage(ProgramFile, Result) then
    RaiseException('旧程序无法移动，尚未停下旧服务；请检查程序目录权限或其他程序的文件占用后重试。');
end;

function RemoveProgramImage(ProgramFile: String): Boolean;
var
  Attempt: Integer;
  ErrorCode: LongWord;
begin
  Result := False;
  for Attempt := 1 to 50 do begin
    if NativeDeleteFile(ProgramFile) then begin
      Result := True;
      Exit;
    end;
    ErrorCode := NativeLastError;
    if ErrorCode = 2 then begin
      Result := True;
      Exit;
    end;
    if (ErrorCode <> 5) and (ErrorCode <> 32) and (ErrorCode <> 33) then Break;
    // Inno otherwise records success even when a short-lived sharing lock leaves the program behind.
    Sleep(100);
  end;
  Log('Could not delete program image; Windows error: ' + IntToStr(ErrorCode));
end;

function RestoreImage(var Previous: String; Destination: String): Boolean;
begin
  Result := True;
  if (Previous <> '') and not FileExists(Destination) then begin
    Result := MoveProgramImage(Previous, Destination);
    if Result then Previous := '';
  end;
end;

function RestorePreviousPrograms: Boolean;
var
  WindowRestored: Boolean;
begin
  WindowRestored := RestoreImage(PreviousWindow, ExpandConstant('{app}\lgmon-window.exe'));
  Result := RestoreImage(PreviousProgram, ExpandConstant('{app}\lgmon.exe')) and WindowRestored;
end;

#ifndef FixtureMode
const
  ServiceKey = 'SYSTEM\CurrentControlSet\Services\lgmon';
  UpgradeProxyKey = 'SYSTEM\CurrentControlSet\Control\lg-monitor-upgrade';

function NativeCreateRegistryKey(Root: LongWord; SubKey: String; Reserved, ClassPtr, Options, Access: LongWord;
  var Attributes: TSetupSecurityAttributes; var Key, Disposition: LongWord): Longint;
  external 'RegCreateKeyExW@advapi32.dll stdcall';
function NativeSetKeySecurity(Key, Info, Descriptor: LongWord): Longint;
  external 'RegSetKeySecurity@advapi32.dll stdcall';
function NativeCloseKey(Key: LongWord): Longint;
  external 'RegCloseKey@advapi32.dll stdcall';

procedure SecureUpgradeProxyKey;
var
  Attributes: TSetupSecurityAttributes;
  Key, Disposition: LongWord;
begin
  // Proxy URLs can contain credentials. Only administrators and SYSTEM may read the retry checkpoint.
  Attributes.Length := 12;
  Attributes.Inherit := 0;
  if not NativeSecurityDescriptor('D:P(A;;KA;;;SY)(A;;KA;;;BA)', 1, Attributes.Descriptor, 0) then
    RaiseException('无法保护升级代理记录。');
  try
    if NativeCreateRegistryKey($80000002, UpgradeProxyKey, 0, 0, 0, $40102, Attributes, Key, Disposition) <> 0 then
      RaiseException('无法创建受保护的升级代理记录。');
    try
      if NativeSetKeySecurity(Key, $80000004, Attributes.Descriptor) <> 0 then
        RaiseException('无法限制升级代理记录的读取权限。');
    finally
      NativeCloseKey(Key);
    end;
  finally
    NativeLocalFree(Attributes.Descriptor);
  end;
end;

procedure PreserveServiceProxy;
var
  Entries, Entry, Name, Proxies: String;
  I: Integer;
begin
  if not RegValueExists(HKLM64, ServiceKey, 'Environment') then Exit;
  if not RegQueryMultiStringValue(HKLM64, ServiceKey, 'Environment', Entries) then
    RaiseException('无法读取原服务环境，已停止升级。');
  while Entries <> '' do begin
    I := Pos(#0, Entries);
    if I = 0 then I := Length(Entries) + 1;
    Entry := Copy(Entries, 1, I - 1);
    Delete(Entries, 1, I);
    Name := Uppercase(Copy(Entry, 1, Pos('=', Entry) - 1));
    if (Name = 'HTTPS_PROXY') or (Name = 'HTTP_PROXY') or (Name = 'NO_PROXY') or (Name = 'ALL_PROXY') then begin
      if Proxies <> '' then Proxies := Proxies + #0;
      Proxies := Proxies + Entry;
    end;
  end;
  // Service removal deletes its Environment. Keep the matching home's proxy until the replacement starts.
  if Proxies <> '' then begin
    SecureUpgradeProxyKey;
    if not RegWriteMultiStringValue(HKLM64, UpgradeProxyKey, DataHome(''), Proxies) then
      RaiseException('无法保留原服务的代理配置，已停止升级。');
  end;
end;

procedure ClearUpgradeProxy(Home: String);
begin
  if RegValueExists(HKLM64, UpgradeProxyKey, Home) then begin
    RegDeleteValue(HKLM64, UpgradeProxyKey, Home);
    RegDeleteKeyIfEmpty(HKLM64, UpgradeProxyKey);
  end;
end;
#endif

procedure StopProgramsIn(Directory: String; WindowOnly: Boolean); forward;

function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  TemporaryProgram, Parameters, Text, Directory, Body, Expected: String;
  Attributes: TSetupSecurityAttributes;
begin
  Result := '';
  try
#ifdef FixtureMode
    Expected := '{#FixtureRoot}\program';
#else
    Expected := ExpandConstant('{autopf64}\lg-monitor');
#endif
    if CompareText(RemoveBackslashUnlessRoot(ExpandFileName(WizardDirValue)), RemoveBackslashUnlessRoot(ExpandFileName(Expected))) <> 0 then
      RaiseException('服务程序目录固定，不能通过 /DIR 改到其他位置。');
    Log('lgmon-stage: check');
    AcquireGuard;
    ExtractTemporaryFile('lgmon.exe');
    TemporaryProgram := ExpandConstant('{tmp}\lgmon.exe');
    Parameters := BaseParameters('check');
    if HasSwitch('/LGMONUPGRADE') then Parameters := Parameters + ' --upgrade';
    if not HasMonitor and ReusePair.Checked then Parameters := Parameters + ' --require-paired';
    if not Helper(TemporaryProgram, Parameters, Text) then RaiseException(Text);
    if not DirExists(Expected) then begin
      // Inno Setup 6 has a 32-bit stub, so SECURITY_ATTRIBUTES contains three 32-bit fields.
      Attributes.Length := 12;
      Attributes.Descriptor := 0;
      Attributes.Inherit := 0;
      if not NativeSecurityDescriptor('O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;GRGX;;;BU)', 1, Attributes.Descriptor, 0) then
        RaiseException('无法创建程序目录权限；未复制程序或注册服务。');
      try
        if not NativeCreateDirectory(Expected, Attributes) then
          RaiseException('无法创建受保护程序目录；未复制程序或注册服务。');
      finally
        NativeLocalFree(Attributes.Descriptor);
      end;
      if not Helper(TemporaryProgram, Parameters, Text) then RaiseException(Text);
    end;
    if HasMonitor then begin
      if not Helper(TemporaryProgram, 'setup check --panel-url --home ' + Quoted(DataHome('')), CachedPanel) then RaiseException(CachedPanel);
      if (Pos('http://', Lowercase(CachedPanel)) <> 1) and (Pos('https://', Lowercase(CachedPanel)) <> 1) then
        RaiseException('本机面板地址无效，未开始安装。');
    end;
    if not HasMonitor and not ReusePair.Checked and (PairInput = '') then begin
      Directory := ExpandConstant('{tmp}\lgmon-pair-input');
      if not Helper(TemporaryProgram, 'setup inbox --directory ' + Quoted(Directory), Text) then RaiseException(Text);
      PairInput := Directory + '\pair.json';
      Body := '{"code":' + JsonString(Trim(PairPage.Values[1])) + ',"fingerprint":' + JsonString(Trim(PairPage.Values[2])) + '}';
      if not SaveStringToFile(PairInput, Utf8Encode(Body), False) then RaiseException('无法写入受保护的配对输入，没有注册服务。');
      Body := '';
    end;
    if HasSwitch('/LGMONUPGRADE') and FileExists(Expected + '\lgmon.exe') then begin
      // Confirm that the old image can move before removing its service. MCP sessions keep the same image and pipes.
      Log('lgmon-stage: upgrade');
#ifndef FixtureMode
      PreserveServiceProxy;
#endif
      CleanPreviousPrograms(Expected);
      PreviousProgram := RetireProgram(Expected + '\lgmon.exe');
      // The window can hold its executable after closing. Reserve both destination paths before stopping the service.
      if FileExists(Expected + '\lgmon-window.exe') then
        PreviousWindow := RetireProgram(Expected + '\lgmon-window.exe');
      ReleaseGuard;
      if not Helper(PreviousProgram, 'setup remove --home ' + Quoted(DataHome('')) + ' --confirm', Text) then
        RaiseException('停止旧版本失败，已保留配置和数据。' + #13#10 + Text);
      AcquireGuard;
      StopProgramsIn(Expected, True);
    end;
  except
    Result := GetExceptionMessage;
    if not RestorePreviousPrograms then
      Result := Result + #13#10 + '未能还原全部程序文件，请保留数据及程序目录中的 .previous 文件。';
    ReleaseGuard;
  end;
end;

procedure CurStepChanged(CurStep: TSetupStep);
var
  Parameters, Text, Metadata, Receipt: String;
  I: Integer;
  MetadataHandle: THandle;
  Written, ByteCount: LongWord;
begin
  if CurStep = ssPostInstall then begin
    // The profile APIs otherwise create ANSI files, losing paths outside the system code page.
    Metadata := #$FEFF + '[Setup]' + #13#10 + 'Product=lg-monitor' + #13#10 +
      'Home=' + DataHome('') + #13#10 + 'Role=' + SelectedRole('') + #13#10;
    // With the address here, an update that finds the service already gone can still keep it.
    if HasMonitor then Metadata := Metadata + 'Listen=' + Trim(ListenPage.Values[0]) + #13#10
    else Metadata := Metadata + 'Monitor=' + Trim(PairPage.Values[0]) + #13#10;
    // Keep the first distribution receipt across upgrades; ordinary GitHub downloads have none.
    if FileExists(ExpandConstant('{app}\setup.ini')) then
      Receipt := GetIniString('Setup', 'DownloadReceipt', '', ExpandConstant('{app}\setup.ini'))
    else Receipt := ExpandConstant('{param:LGMONRECEIPT|}');
    if Length(Receipt) = 32 then begin
      for I := 1 to Length(Receipt) do
        if Pos(Copy(Receipt, I, 1), '0123456789abcdef') = 0 then begin
          Receipt := '';
          Break;
        end;
      if Receipt <> '' then Metadata := Metadata + 'DownloadReceipt=' + Receipt + #13#10;
    end;
    if HasSwitch('/LGMONUPGRADE') and FileExists(ExpandConstant('{app}\setup.ini')) then begin
      // The checked installation's ownership file must be recreated for the new role and home.
      if not DeleteFile(ExpandConstant('{app}\setup.ini')) then
        RaiseException('无法替换旧安装归属文件；未注册新服务，请保留 home 并检查程序目录。');
    end;
    MetadataHandle := NativeCreateFile(ExpandConstant('{app}\setup.ini'), $40000000, 0, 0, 1, $80, 0);
    if MetadataHandle = THandle(-1) then RaiseException('无法创建安装归属文件；未注册服务，请保留 home 并检查程序目录。');
    try
      ByteCount := Length(Metadata) * 2;
      if not NativeWriteFile(MetadataHandle, Metadata, ByteCount, Written, 0) or (Written <> ByteCount) then
        RaiseException('无法完整保存安装归属文件；未注册服务，请保留 home。');
    finally
      NativeCloseHandle(MetadataHandle);
    end;
    ReleaseGuard;
    Log('lgmon-stage: service');
    Parameters := BaseParameters('install') + ' --confirm';
    if not HasMonitor and not ReusePair.Checked then Parameters := Parameters + ' --pair-input ' + Quoted(PairInput);
    if not Helper(ExpandConstant('{app}\lgmon.exe'), Parameters, Text) then
      RaiseException('服务安装未完成。保留 home，使用卸载程序移除本次程序后可重新安装。' + #13#10 + Text);
    AcquireGuard;
#ifndef FixtureMode
    ClearUpgradeProxy(DataHome(''));
#endif
    SetupSucceeded := True;
  end;
end;

function GetCustomSetupExitCode: Integer;
begin
  if SetupSucceeded then Result := 0 else Result := 20;
end;

function InitializeUninstall: Boolean;
begin
  Result := False;
  try
    if GetIniString('Setup', 'Product', '', ExpandConstant('{app}\setup.ini')) <> 'lg-monitor' then
      RaiseException('安装归属信息缺失，请保留程序并人工检查 lgmon service status。');
    UninstallHome := GetIniString('Setup', 'Home', '', ExpandConstant('{app}\setup.ini'));
    if UninstallHome = '' then RaiseException('安装数据目录信息缺失，拒绝卸载。');
    AcquireGuard;
    Result := True;
  except
    SuppressibleMsgBox(GetExceptionMessage, mbError, MB_OK, IDOK);
  end;
end;

// Every lgmon program still running from the program directory: lgmon's window (also one an older service opened
// as SYSTEM), agents' MCP sessions and retired images. One left running made Windows delete its file only at the
// next restart, and every later install then refused to start until the computer was restarted.
procedure StopProgramsIn(Directory: String; WindowOnly: Boolean);
var
  Locator, Service, Processes, Item: Variant;
  Index: Integer;
  Path, FileName: String;
begin
  try
    Locator := CreateOleObject('WbemScripting.SWbemLocator');
    Service := Locator.ConnectServer('.', 'root\CIMV2');
    Processes := Service.ExecQuery('SELECT ProcessId, ExecutablePath FROM Win32_Process WHERE Name LIKE ''lgmon%''');
    for Index := 0 to Processes.Count - 1 do begin
      Item := Processes.ItemIndex(Index);
      if not VarIsNull(Item.ExecutablePath) then begin
        Path := Item.ExecutablePath;
        FileName := Lowercase(ExtractFileName(Path));
        if (CompareText(RemoveBackslashUnlessRoot(ExtractFileDir(Path)), RemoveBackslashUnlessRoot(Directory)) = 0) and
            (not WindowOnly or (FileName = 'lgmon-window.exe') or (Pos('lgmon-window.exe.previous-', FileName) = 1)) then begin
          Log('Stopping lgmon program still running: ' + Path);
          Item.Terminate(0);
        end;
      end;
    end;
  except
    Log('Could not list running lgmon programs: ' + GetExceptionMessage);
  end;
end;

// The window's browser profiles: this person's, and one an older service created under the system profile.
procedure RemoveWindowProfiles;
begin
#ifndef FixtureMode
  DelTree(ExpandConstant('{localappdata}\lg-monitor\window'), True, True, True);
  RemoveDir(ExpandConstant('{localappdata}\lg-monitor'));
  DelTree(ExpandConstant('{sys}\config\systemprofile\AppData\Local\lg-monitor'), True, True, True);
#endif
end;

// Only lgmon's own folders go, so a home someone pointed at a shared folder keeps everything else in it.
procedure RemoveDataHome(Home: String);
begin
  if Home = '' then Exit;
  DelTree(Home + '\config', True, True, True);
  DelTree(Home + '\data', True, True, True);
  DelTree(Home + '\logs', True, True, True);
  DelTree(Home + '\scripts', True, True, True);
  if not RemoveDir(Home) then Log('Data home kept because it is not empty: ' + Home);
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);var
  Text, TemporaryProgram: String;
begin
  if CurUninstallStep = usUninstall then begin
    // Config and data stay unless the person asks; a scripted removal asks with /LGMONPURGE.
    if UninstallSilent then PurgeData := HasSwitch('/LGMONPURGE')
    else PurgeData := MsgBox('要同时删除 lgmon 的配置和监控数据吗？' + #13#10 + #13#10 + UninstallHome + #13#10 + #13#10 +
      '选“否”会保留它们，以后重新安装可以接着用。', mbConfirmation, MB_YESNO or MB_DEFBUTTON2) = IDYES;
    // The last helper must not load the image that the uninstaller immediately deletes.
    TemporaryProgram := ExpandConstant('{tmp}\lgmon-remove.exe');
    if not FileCopy(ExpandConstant('{app}\lgmon.exe'), TemporaryProgram, False) then
      RaiseException('无法创建卸载辅助程序，尚未停止服务或删除程序。');
    ReleaseGuard;
    if not Helper(TemporaryProgram, 'setup remove --release-ports --home ' + Quoted(UninstallHome) + ' --confirm', Text) then RaiseException(Text);
    AcquireGuard;
    if not Helper(TemporaryProgram, 'setup check --service-absent --home ' + Quoted(UninstallHome), Text) then RaiseException(Text);
    StopProgramsIn(ExpandConstant('{app}'), False);
    if not RemoveProgramImage(ExpandConstant('{app}\lgmon.exe')) then
      RaiseException('服务已移除，但程序文件仍被占用，尚未完成卸载。配置和数据保留，请关闭使用 lgmon 的连接后重新卸载。');
    if not RemoveProgramImage(ExpandConstant('{app}\lgmon-window.exe')) then
      RaiseException('服务已移除，但 lgmon 窗口仍在运行，尚未完成卸载。配置和数据保留，请关闭 lgmon 窗口后重新卸载。');
    CleanPreviousPrograms(ExpandConstant('{app}'));
  end;
  if CurUninstallStep = usPostUninstall then begin
    RemoveWindowProfiles;
#ifndef FixtureMode
    ClearUpgradeProxy(UninstallHome);
#endif
    if PurgeData then RemoveDataHome(UninstallHome);
  end;
end;

procedure DeinitializeSetup;
begin
  if SetupSucceeded then CleanPreviousPrograms(ExpandConstant('{app}'))
  else if not RestorePreviousPrograms then Log('Could not restore previous programs: ' + PreviousProgram + ' ' + PreviousWindow);
  if Assigned(PairPage) then begin
    PairPage.Values[1] := '';
    PairPage.Values[2] := '';
  end;
  CloseGuard;
end;

procedure DeinitializeUninstall;
begin
  CloseGuard;
end;
