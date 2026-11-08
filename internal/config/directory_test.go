package config

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestWindowsDirectoriesUseStableLexicalRules(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{`d:/site/./blog/../blog/`, `D:\site\blog`}, {`C:\`, `C:\`},
		{`C:\..\site`, `C:\site`}, {`\\server\share\app\..\blog\`, `\\server\share\blog`},
		{`//server/share/../../blog`, `\\server\share\blog`}, {`\\server\share\`, `\\server\share`},
		{`D:\%APP%\blog`, `D:\%APP%\blog`},
	} {
		got, err := CleanWindowsDirectory(test.input)
		if err != nil || got != test.want {
			t.Fatalf("normalize %q: got %q %v, want %q", test.input, got, err, test.want)
		}
	}
	for _, input := range []string{"", "D:", "D:site", `\site`, "site", `\\server`, `\\server\\site`, `D:\a:b`, `D:\a*`, `D:\a?`, `D:\a"`, `D:\a<`, `D:\a>`, `D:\a|`, "D:\\a\x00b", `\\?\D:\site`} {
		if _, err := CleanWindowsDirectory(input); err == nil {
			t.Errorf("invalid directory accepted: %q", input)
		}
	}
	for _, test := range []struct{ input, want string }{
		{`\\?\d:\site\.\blog\`, `D:\site\blog`},
		{`\\?\UNC\server\share\app\..\blog\`, `\\server\share\blog`},
	} {
		got, err := ObservedWindowsDirectory(test.input)
		if err != nil || got != test.want {
			t.Fatalf("observed directory %q: %q %v", test.input, got, err)
		}
	}
	for _, test := range []struct{ input, want string }{
		{`C:\ping.exe`, `C:\`}, {`\\?\C:\Windows\System32\ping.exe`, `C:\Windows\System32`},
		{`\\?\UNC\server\share\bin\app.exe`, `\\server\share\bin`},
	} {
		got, err := ExecutableDirectory(test.input)
		if err != nil || got != test.want {
			t.Fatalf("executable directory %q: %q %v", test.input, got, err)
		}
	}
	for _, test := range []struct {
		root, value string
		want        bool
	}{
		{`D:\site`, `d:\SITE`, true}, {`D:\site`, `d:\site\blog`, true},
		{`D:\site`, `D:\sites\blog`, false}, {`D:\site`, `E:\site`, false},
		{`D:\`, `d:\site`, true}, {`\\server\share`, `\\SERVER\SHARE\blog`, true},
		{`\\server\share`, `\\server\shares\blog`, false},
	} {
		if got := WindowsDirectoryContains(test.root, test.value); got != test.want {
			t.Errorf("contains %q %q: got %t", test.root, test.value, got)
		}
	}
}

func TestDirectoryConditionsValidateWithFieldLocations(t *testing.T) {
	for _, spec := range []Match{
		{ExeDir: `D:\tools`}, {Process: "node.exe", WorkDir: `D:\site`},
		{Cmdline: "blog", WorkDir: `\\server\share\site`}, {ExeDir: `D:\tools`, WorkDir: `D:\site`},
	} {
		if err := ValidateTargetMatch(spec); err != nil {
			t.Fatalf("valid directory match rejected: %+v %v", spec, err)
		}
	}
	for _, spec := range []Match{{WorkDir: `D:\site`}, {Service: "fake", WorkDir: `D:\site`}, {ExeDir: "D:site"}, {Process: "node.exe", WorkDir: "site"}} {
		if err := ValidateTargetMatch(spec); err == nil {
			t.Fatalf("invalid directory match accepted: %+v", spec)
		}
	}
	for _, field := range []string{"exeDir", "workDir"} {
		files := fstest.MapFS{"targets.yaml": {Data: []byte("targets:\n  - id: bad\n    match:\n      process: node.exe\n      " + field + ": relative\n")}}
		if _, err := Load(files); err == nil || !strings.Contains(err.Error(), "第 5 行") {
			t.Fatalf("missing directory location for %s: %v", field, err)
		}
	}
}
