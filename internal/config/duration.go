package config

import (
	"math/big"
	"regexp"
	"strings"
	"time"
)

var dayDuration = regexp.MustCompile(`([0-9]+(\.[0-9]*)?|\.[0-9]+)d`)

func parseDuration(text string) (time.Duration, error) {
	if strings.Contains(text, "d") {
		text = dayDuration.ReplaceAllStringFunc(text, func(token string) string {
			value, _ := new(big.Rat).SetString(strings.TrimSuffix(token, "d"))
			value.Mul(value, new(big.Rat).SetInt64(int64(24*time.Hour)))
			// Convert decimal days exactly; float rounding loses precision in long retention windows.
			nanos := new(big.Int).Quo(value.Num(), value.Denom())
			return nanos.String() + "ns"
		})
	}
	return time.ParseDuration(text)
}
