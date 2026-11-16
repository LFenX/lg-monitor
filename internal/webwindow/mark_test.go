package webwindow

import "testing"

func TestTheMarkIsAWarmTileWithBarsAndSoftCorners(t *testing.T) {
	const size = 32
	pixels := DrawMark(size)
	at := func(x, y int) []byte { return pixels[(y*size+x)*4 : (y*size+x)*4+4] }
	if corner := at(0, 0); corner[3] != 0 {
		t.Fatalf("corner is painted: %v", corner)
	}
	if tile := at(3, 16); tile[0] != markTile[0] || tile[1] != markTile[1] || tile[2] != markTile[2] || tile[3] != 255 {
		t.Fatalf("tile pixel %v", tile)
	}
	// The newest bar is fully bright; the oldest lets more of the tile through.
	newest, oldest := at(23, 16), at(8, 16)
	if newest[0] != markBar[0] || oldest[0] >= newest[0] || oldest[0] <= markTile[0] {
		t.Fatalf("bars newest=%v oldest=%v", newest, oldest)
	}
	soft := 0
	for y := 0; y < 9; y++ {
		for x := 0; x < 9; x++ {
			if alpha := at(x, y)[3]; alpha > 0 && alpha < 255 {
				soft++
			}
		}
	}
	if soft < 4 {
		t.Fatalf("rounded corner is not smoothed: %d partly covered pixels", soft)
	}
}
