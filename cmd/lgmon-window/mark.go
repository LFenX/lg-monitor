package main

import "math"

// The mark from web/src/brand.tsx on a 32-unit grid: a warm tile with four heartbeat bars, the newest brightest.
var (
	markTile    = [3]uint8{0xc9, 0x64, 0x42}
	markBar     = [3]uint8{0xfa, 0xf9, 0xf5}
	markOpacity = [4]float64{0.42, 0.6, 0.8, 1}
)

// insideRoundRect reports whether (x, y) lies in the rounded rectangle, all in mark units.
func insideRoundRect(x, y, left, top, width, height, radius float64) bool {
	if x < left || y < top || x > left+width || y > top+height {
		return false
	}
	cx := math.Max(left+radius, math.Min(x, left+width-radius))
	cy := math.Max(top+radius, math.Min(y, top+height-radius))
	return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= radius*radius
}

// drawMark paints the mark at size×size pixels as straight-alpha RGBA, top row first. Each pixel is sampled 4×4
// times, which is enough to keep the corners and bars smooth at icon sizes.
func drawMark(size int) []byte {
	const samples = 4
	pixels := make([]byte, size*size*4)
	scale := 32 / float64(size)
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var tile, bar float64
			for sy := 0; sy < samples; sy++ {
				for sx := 0; sx < samples; sx++ {
					x := (float64(px) + (float64(sx)+0.5)/samples) * scale
					y := (float64(py) + (float64(sy)+0.5)/samples) * scale
					if !insideRoundRect(x, y, 0, 0, 32, 32, 9) {
						continue
					}
					tile++
					for index, opacity := range markOpacity {
						if insideRoundRect(x, y, 7+float64(index)*5, 9.5, 3, 13, 1.5) {
							bar += opacity
						}
					}
				}
			}
			if tile == 0 {
				continue
			}
			// Bars sit on the tile, so their colour mixes with the tile's within the covered samples.
			mix := bar / tile
			offset := (py*size + px) * 4
			for channel := 0; channel < 3; channel++ {
				pixels[offset+channel] = uint8(math.Round(float64(markTile[channel])*(1-mix) + float64(markBar[channel])*mix))
			}
			pixels[offset+3] = uint8(math.Round(255 * tile / (samples * samples)))
		}
	}
	return pixels
}
