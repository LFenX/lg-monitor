//go:build !setuppayload

package main

// engine is empty in ordinary builds and tests; --engine names the installer instead.
var engine []byte
