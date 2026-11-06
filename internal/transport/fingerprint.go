package transport

import (
	"encoding/hex"
	"fmt"
	"strings"
)

func normalizeFingerprint(value string) string {
	return strings.NewReplacer(":", "", "-", "").Replace(strings.ToUpper(strings.TrimSpace(value)))
}

func FingerprintShort(value string) string {
	value = normalizeFingerprint(value)
	if len(value) != 16 && len(value) != 64 {
		return ""
	}
	if _, err := hex.DecodeString(value); err != nil {
		return ""
	}
	return fmt.Sprintf("%s-%s-%s-%s", value[:4], value[4:8], value[8:12], value[12:16])
}

func VerifyFingerprint(actual, expected string) error {
	actual, expected = normalizeFingerprint(actual), normalizeFingerprint(expected)
	if len(expected) != 16 && len(expected) != 64 {
		return fmt.Errorf("请输入 16 位短指纹或完整 64 位十六进制指纹，8 位前缀不再接受：%w", ErrFingerprint)
	}
	if _, err := hex.DecodeString(expected); err != nil {
		return fmt.Errorf("指纹必须为 16 位或 64 位十六进制字符：%w", ErrFingerprint)
	}
	if len(actual) != 64 || !strings.HasPrefix(actual, expected) {
		return ErrFingerprint
	}
	if _, err := hex.DecodeString(actual); err != nil {
		return ErrFingerprint
	}
	return nil
}
