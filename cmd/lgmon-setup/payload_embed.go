//go:build setuppayload

package main

import _ "embed"

// engine is the Inno Setup installer; build-installer.ps1 places it in payload/ only for this build.
//
//go:embed payload/engine.exe
var engine []byte
