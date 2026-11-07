package collector

import (
	"fmt"
	"math"
)

const ClockSkewThresholdMS = 10000

func ClockSkewed(offset int64) bool {
	return offset > ClockSkewThresholdMS || offset < -ClockSkewThresholdMS
}

func ClockWarning(offset int64) string {
	if !ClockSkewed(offset) {
		return ""
	}
	direction := "慢"
	if offset < 0 {
		direction = "快"
	}
	return fmt.Sprintf("这台电脑的时钟比监控器%s %.0f 秒，请同步时间", direction, math.Ceil(math.Abs(float64(offset))/1000))
}
