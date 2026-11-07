//go:build windows && integration

package notify

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"
)

func TestNativeToastHelperParsesWithoutDisplayingOrRegistering(t *testing.T) {
	system := windowsToastSystem{}
	session, err := system.CurrentSession()
	if err != nil {
		t.Fatal(err)
	}
	if session == 0 {
		t.Skip("interactive session is required; service-session selection uses fake system tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	source := toastScript([]byte(`<toast><visual><binding template="ToastGeneric"><text>语法验证</text></binding></visual></toast>`))
	// ParseInput never compiles the WinRT type references or executes the notification body.
	check := `$source = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('` + base64.StdEncoding.EncodeToString([]byte(source)) + `')); $tokens = $null; $errors = $null; [void][System.Management.Automation.Language.Parser]::ParseInput($source, [ref]$tokens, [ref]$errors); if ($errors.Count -ne 0) { exit 1 }; exit 0`
	started := time.Now()
	exit, err := system.Run(ctx, session, check)
	t.Logf("parser helper elapsed: %s", time.Since(started))
	if err != nil || exit != 0 {
		t.Fatalf("parse=%d,%v", exit, err)
	}
	second, cancelSecond := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelSecond()
	started = time.Now()
	exit, err = system.Run(second, session, "exit 2")
	t.Logf("exit helper elapsed: %s", time.Since(started))
	if err != nil || exit != 2 {
		t.Fatalf("exit status=%d,%v", exit, err)
	}
	probe, cancelProbe := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelProbe()
	load := `$ErrorActionPreference = 'Stop'; try { [Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null; [Windows.UI.Notifications.ToastNotification, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null; [Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime] | Out-Null; $document = New-Object Windows.Data.Xml.Dom.XmlDocument; $document.LoadXml('<toast/>'); exit 0 } catch { exit 4 }`
	exit, err = system.Run(probe, session, load)
	if err != nil || exit != 0 {
		t.Fatalf("WinRT metadata and XML document=%d,%v", exit, err)
	}
}

func TestNativeToastHelperCancelsOnlyItsOwnProcess(t *testing.T) {
	system := windowsToastSystem{}
	session, err := system.CurrentSession()
	if err != nil {
		t.Fatal(err)
	}
	if session == 0 {
		t.Skip("interactive session is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err = system.Run(ctx, session, "Start-Sleep -Seconds 60")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("helper cancellation=%v", err)
	}
}
