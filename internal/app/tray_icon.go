// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
)

// trayIcon draws the brand mark, four heartbeat bars on a rounded tile, in the colour of the
// current state, so the taskbar shows the same symbol as the web page.
func trayIcon(state TrayState) ([]byte, error) {
	background := color.RGBA{R: 138, G: 136, B: 127, A: 255}
	switch state {
	case TrayHealthy:
		background = color.RGBA{R: 47, G: 179, B: 107, A: 255}
	case TrayFiring:
		background = color.RGBA{R: 220, G: 53, B: 69, A: 255}
	case TrayWarning:
		background = color.RGBA{R: 240, G: 160, B: 32, A: 255}
	case TrayStarting:
		background = color.RGBA{R: 201, G: 100, B: 66, A: 255}
	}
	sizes := []int{16, 32}
	result := make([]byte, 6+16*len(sizes))
	binary.LittleEndian.PutUint16(result[2:], 1)
	binary.LittleEndian.PutUint16(result[4:], uint16(len(sizes)))
	// Bars in tile coordinates (-1..1): x start, and the white opacity that fades in towards "now".
	bars := [][2]float64{{-0.5625, .45}, {-0.25, .62}, {0.0625, .8}, {0.375, 1}}
	const width, top, bottom, edge, corner = 0.1875, -0.406, 0.406, 0.94, 0.55
	paint := func(px, py float64) (inside bool, white float64) {
		dx, dy := max(math.Abs(px)-(edge-corner), 0), max(math.Abs(py)-(edge-corner), 0)
		if math.Abs(px) > edge || math.Abs(py) > edge || dx*dx+dy*dy > corner*corner {
			return false, 0
		}
		if py < top || py > bottom {
			return true, 0
		}
		if state == TrayPaused {
			if (px >= -0.34 && px <= -0.1) || (px >= 0.1 && px <= 0.34) {
				return true, 1
			}
			return true, 0
		}
		for _, bar := range bars {
			if px >= bar[0] && px <= bar[0]+width {
				return true, bar[1]
			}
		}
		return true, 0
	}
	const samples = 4
	for i, size := range sizes {
		img := image.NewRGBA(image.Rect(0, 0, size, size))
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				var coverage, light float64
				for sy := 0; sy < samples; sy++ {
					for sx := 0; sx < samples; sx++ {
						px := (float64(x)+(float64(sx)+.5)/samples)*2/float64(size) - 1
						py := (float64(y)+(float64(sy)+.5)/samples)*2/float64(size) - 1
						if inside, white := paint(px, py); inside {
							coverage++
							light += white
						}
					}
				}
				if coverage == 0 {
					continue
				}
				mix := light / coverage
				channel := func(base uint8) uint8 { return uint8(float64(base)*(1-mix) + 250*mix) }
				alpha := coverage / (samples * samples)
				// Premultiplied, as image.RGBA expects.
				img.SetRGBA(x, y, color.RGBA{R: uint8(float64(channel(background.R)) * alpha), G: uint8(float64(channel(background.G)) * alpha), B: uint8(float64(channel(background.B)) * alpha), A: uint8(255 * alpha)})
			}
		}
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, img); err != nil {
			return nil, fmt.Errorf("生成托盘图标失败：%w", err)
		}
		entry := result[6+i*16 : 6+(i+1)*16]
		entry[0], entry[1] = byte(size), byte(size)
		binary.LittleEndian.PutUint16(entry[4:], 1)
		binary.LittleEndian.PutUint16(entry[6:], 32)
		binary.LittleEndian.PutUint32(entry[8:], uint32(encoded.Len()))
		binary.LittleEndian.PutUint32(entry[12:], uint32(len(result)))
		result = append(result, encoded.Bytes()...)
	}
	return result, nil
}
