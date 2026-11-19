// Package profile describes this computer's account: the nickname and avatar other people see
// on the network instead of the computer's name.
package profile

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"math/big"
	"slices"
	"strings"
	"unicode/utf8"
)

// Presets are the avatars bundled with the web panel (from fiora, MIT). Only the key travels
// between computers; each one draws its own copy.
var Presets = []string{
	"fiora-0", "fiora-1", "fiora-2", "fiora-3", "fiora-4", "fiora-5", "fiora-6", "fiora-7",
	"fiora-8", "fiora-9", "fiora-10", "fiora-11", "fiora-12", "fiora-13", "fiora-14",
	"huaji-1", "huaji-18", "huaji-33", "huaji-34", "huaji-36",
}

const (
	// MaxNickname keeps a name readable in a sidebar row.
	MaxNickname = 24
	// MaxAvatar bounds an uploaded picture as a data URL; the panel shrinks pictures to 160 px
	// before sending, which lands far below it, and every board refresh carries it.
	MaxAvatar = 64 * 1024
)

var avatarTypes = map[string][]byte{
	"image/png":  []byte("\x89PNG\r\n\x1a\n"),
	"image/jpeg": {0xff, 0xd8, 0xff},
	"image/gif":  []byte("GIF8"),
	"image/webp": []byte("RIFF"),
}

// CheckNickname trims a nickname and reports why it cannot be used.
func CheckNickname(value string) (string, error) {
	value = strings.Join(strings.Fields(value), " ")
	switch {
	case value == "":
		return "", fmt.Errorf("昵称不能为空")
	case utf8.RuneCountInString(value) > MaxNickname:
		return "", fmt.Errorf("昵称最多 %d 个字", MaxNickname)
	case strings.ContainsAny(value, "<>\x00"):
		return "", fmt.Errorf("昵称不能包含 < > 等符号")
	}
	return value, nil
}

// CheckAvatar accepts a preset key or a small picture sent as a data URL whose bytes really are
// the image type it claims.
func CheckAvatar(value string) error {
	if slices.Contains(Presets, value) {
		return nil
	}
	if len(value) > MaxAvatar {
		return fmt.Errorf("头像图片太大，请换一张小一点的图片")
	}
	kind, data, ok := strings.Cut(strings.TrimPrefix(value, "data:"), ";base64,")
	magic, known := avatarTypes[kind]
	if !strings.HasPrefix(value, "data:") || !ok || !known {
		return fmt.Errorf("头像只能是内置头像，或 PNG、JPEG、GIF、WebP 图片")
	}
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil || !bytes.HasPrefix(decoded, magic) || kind == "image/webp" && (len(decoded) < 12 || string(decoded[8:12]) != "WEBP") {
		return fmt.Errorf("头像图片内容和格式对不上，请换一张图片")
	}
	return nil
}

// RandomAvatar picks a preset other than the current one, so a click always shows a change.
func RandomAvatar(random io.Reader, current string) (string, error) {
	choices := slices.DeleteFunc(slices.Clone(Presets), func(key string) bool { return key == current })
	index, err := pick(random, len(choices))
	if err != nil {
		return "", err
	}
	return choices[index], nil
}

var (
	moods   = []string{"晴天", "安静", "认真", "机灵", "爽朗", "稳重", "好奇", "温和", "敏捷", "勤快", "淡定", "热心"}
	animals = []string{"水獭", "海豚", "松鼠", "柴犬", "狐狸", "熊猫", "刺猬", "企鹅", "考拉", "白鹭", "海豹", "猫头鹰"}
)

// RandomNickname names a new account until its person picks a name; it never uses the
// computer's name, which says nothing to the people looking at it.
func RandomNickname(random io.Reader) (string, error) {
	mood, err := pick(random, len(moods))
	if err != nil {
		return "", err
	}
	animal, err := pick(random, len(animals))
	if err != nil {
		return "", err
	}
	return moods[mood] + "的" + animals[animal], nil
}

func pick(random io.Reader, n int) (int, error) {
	if random == nil {
		random = rand.Reader
	}
	value, err := rand.Int(random, big.NewInt(int64(n)))
	if err != nil {
		return 0, fmt.Errorf("生成随机头像失败：%w", err)
	}
	return int(value.Int64()), nil
}
