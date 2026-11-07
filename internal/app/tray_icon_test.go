package app

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"testing"
)

func TestTrayICOContainsTransparentIconsWithDifferentAlertColors(t *testing.T) {
	seen := map[[3]uint32]bool{}
	for _, state := range []TrayState{TrayStarting, TrayHealthy, TrayFiring, TrayWarning, TrayUnavailable} {
		data, err := trayIcon(state)
		if err != nil || len(data) < 38 || binary.LittleEndian.Uint16(data[2:]) != 1 || binary.LittleEndian.Uint16(data[4:]) != 2 {
			t.Fatalf("%s: invalid ICO: %v", state, err)
		}
		for i, size := range []int{16, 32} {
			entry := data[6+i*16 : 6+(i+1)*16]
			length, offset := int(binary.LittleEndian.Uint32(entry[8:])), int(binary.LittleEndian.Uint32(entry[12:]))
			if offset < 38 || length < 1 || offset+length > len(data) {
				t.Fatalf("%s: icon directory is outside file bounds", state)
			}
			img, err := png.Decode(bytes.NewReader(data[offset : offset+length]))
			if err != nil || img.Bounds().Dx() != size || img.Bounds().Dy() != size {
				t.Fatalf("%s: invalid %dpx payload: %v", state, size, err)
			}
			_, _, _, alpha := img.At(0, 0).RGBA()
			if alpha != 0 {
				t.Fatalf("%s: icon corners are not transparent", state)
			}
			if size == 32 {
				r, g, b, alpha := img.At(16, 5).RGBA()
				key := [3]uint32{r, g, b}
				if alpha == 0 || seen[key] {
					t.Fatalf("%s: state colors are not distinct", state)
				}
				seen[key] = true
				if state == TrayHealthy && g <= r || state == TrayFiring && r <= g {
					t.Fatalf("%s: unexpected alert color", state)
				}
			}
		}
	}
}

func TestTrayPausedIconHasItsOwnVisibleMark(t *testing.T) {
	paused, err := trayIcon(TrayPaused)
	if err != nil {
		t.Fatal(err)
	}
	disconnected, err := trayIcon(TrayUnavailable)
	if err != nil || bytes.Equal(paused, disconnected) {
		t.Fatal("paused and disconnected icons are identical")
	}
}
