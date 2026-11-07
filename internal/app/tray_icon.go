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

func trayIcon(state TrayState) ([]byte, error) {
	background := color.RGBA{R: 125, G: 139, B: 161, A: 255}
	switch state {
	case TrayHealthy:
		background = color.RGBA{R: 18, G: 171, B: 124, A: 255}
	case TrayFiring:
		background = color.RGBA{R: 239, G: 68, B: 68, A: 255}
	case TrayWarning:
		background = color.RGBA{R: 245, G: 150, B: 35, A: 255}
	case TrayStarting:
		background = color.RGBA{R: 56, G: 125, B: 255, A: 255}
	}
	sizes := []int{16, 32}
	result := make([]byte, 6+16*len(sizes))
	binary.LittleEndian.PutUint16(result[2:], 1)
	binary.LittleEndian.PutUint16(result[4:], uint16(len(sizes)))
	pulse := [][2]float64{{-.68, 0}, {-.36, 0}, {-.15, -.40}, {.05, .45}, {.25, -.15}, {.40, 0}, {.68, 0}}
	for i, size := range sizes {
		img := image.NewRGBA(image.Rect(0, 0, size, size))
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				px, py := (float64(x)+.5)*2/float64(size)-1, (float64(y)+.5)*2/float64(size)-1
				if px*px+py*py > .90*.90 {
					continue
				}
				value := background
				for j := 1; j < len(pulse); j++ {
					if state == TrayPaused {
						break
					}
					a, b := pulse[j-1], pulse[j]
					dx, dy := b[0]-a[0], b[1]-a[1]
					t := min(1.0, max(0.0, ((px-a[0])*dx+(py-a[1])*dy)/(dx*dx+dy*dy)))
					if math.Hypot(px-a[0]-t*dx, py-a[1]-t*dy) <= .075 {
						value = color.RGBA{R: 255, G: 255, B: 255, A: 255}
						break
					}
				}
				if state == TrayPaused && math.Abs(py) <= .45 && ((px >= -.36 && px <= -.12) || (px >= .12 && px <= .36)) {
					value = color.RGBA{R: 255, G: 255, B: 255, A: 255}
				}
				img.SetRGBA(x, y, value)
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
