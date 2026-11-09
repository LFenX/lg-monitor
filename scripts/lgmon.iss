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
DisableWelcomePage=no
DisableDirPage=yes
DisableProgramGroupPage=yes
UsePreviousAppDir=no
UsePreviousTasks=no
CloseApplications=no
RestartApplications=no
RestartIfNeededByRun=no
AlwaysRestart=no
UninstallDisplayIcon={app}\lgmon.exe
UninstallDisplayName={#ProductName}
UninstallLogMode=overwrite
SetupLogging=yes
UninstallLogging=yes

[Languages]
Name: "chinesesimp"; MessagesFile: "ChineseSimplified.isl"

[Messages]
WelcomeLabel2=此向导会安装 lg-monitor，并让您选择角色、数据目录及配对方式。%n%n不会关闭其他程序、修改防火墙或重启电脑。卸载后保留配置、身份和数据。%n%n点击“下一步”开始，点击“取消”退出。

[Files]
Source: "{#PayloadFile}"; DestDir: "{app}"; DestName: "lgmon.exe"; Flags: ignoreversion

[INI]
Filename: "{app}\setup.ini"; Section: "Setup"; Key: "Product"; String: "lg-monitor"
Filename: "{app}\setup.ini"; Section: "Setup"; Key: "Home"; String: "{code:DataHome}"
Filename: "{app}\setup.ini"; Section: "Setup"; Key: "Role"; String: "{code:SelectedRole}"

#ifndef FixtureMode
[Icons]
Name: "{autoprograms}\lg-monitor\监控面板"; Filename: "{code:PanelAddress}"; Check: HasMonitor

[Run]
Filename: "{code:PanelAddress}"; Description: "打开监控面板"; Flags: nowait postinstall skipifsilent shellexec runasoriginaluser; Check: HasMonitor
#endif

[UninstallDelete]
Type: files; Name: "{app}\setup.ini"

[Code]
var
  RolePage: TInputOptionWizardPage;
  HomePage: TInputDirWizardPage;
  ListenPage, PairPage: TInputQueryWizardPage;
  ReusePair: TNewCheckBox;
  GuardHandle: THandle;
  GuardHeld, SetupSucceeded: Boolean;
  PairInput, CachedPanel, UninstallHome: String;

function NativeCreateMutex(Attributes: LongWord; InitialOwner: Boolean; Name: String): THandle;
  external 'CreateMutexW@kernel32.dll stdcall';
function NativeWait(Handle: THandle; Milliseconds: LongWord): LongWord;
  external 'WaitForSingleObject@kernel32.dll stdcall';
function NativeReleaseMutex(Handle: THandle): Boolean;
  external 'ReleaseMutex@kernel32.dll stdcall';
function NativeCloseHandle(Handle: THandle): Boolean;
  external 'CloseHandle@kernel32.dll stdcall';

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

function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  TemporaryProgram, Parameters, Text, Directory, Body, Expected: String;
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
    AcquireGuard;
    ExtractTemporaryFile('lgmon.exe');
    TemporaryProgram := ExpandConstant('{tmp}\lgmon.exe');
    Parameters := BaseParameters('check');
    if not HasMonitor and ReusePair.Checked then Parameters := Parameters + ' --require-paired';
    if not Helper(TemporaryProgram, Parameters, Text) then RaiseException(Text);
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
  except
    Result := GetExceptionMessage;
    ReleaseGuard;
  end;
end;

procedure CurStepChanged(CurStep: TSetupStep);
var
  Parameters, Text: String;
begin
  if CurStep = ssPostInstall then begin
    ReleaseGuard;
    Parameters := BaseParameters('install') + ' --confirm';
    if not HasMonitor and not ReusePair.Checked then Parameters := Parameters + ' --pair-input ' + Quoted(PairInput);
    if not Helper(ExpandConstant('{app}\lgmon.exe'), Parameters, Text) then
      RaiseException('服务安装未完成。保留 home，使用卸载程序移除本次程序后可重新安装。' + #13#10 + Text);
    AcquireGuard;
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

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  Text: String;
begin
  if CurUninstallStep = usUninstall then begin
    ReleaseGuard;
    if not Helper(ExpandConstant('{app}\lgmon.exe'), 'setup remove --home ' + Quoted(UninstallHome) + ' --confirm', Text) then RaiseException(Text);
    AcquireGuard;
    if not Helper(ExpandConstant('{app}\lgmon.exe'), 'setup check --service-absent --home ' + Quoted(UninstallHome), Text) then RaiseException(Text);
  end;
end;

procedure DeinitializeSetup;
begin
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
