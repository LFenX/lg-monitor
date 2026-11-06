package api

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"unicode"
)

type Scope string

const (
	ScopeRead   Scope = "read"
	ScopeConfig Scope = "config"
	ScopeAdmin  Scope = "admin"
)

func (s Scope) Valid() bool { return s == ScopeRead || s == ScopeConfig || s == ScopeAdmin }

func (s Scope) Allows(minimum Scope) bool {
	return s.Valid() && minimum.Valid() && (s == ScopeAdmin || s == minimum || minimum == ScopeRead)
}

type Token struct {
	Value  string   `json:"token"`
	Name   string   `json:"name"`
	Scope  Scope    `json:"scope"`
	Digest [32]byte `json:"-"`
}

func TokenHash(value string) [32]byte { return sha256.Sum256([]byte(value)) }

func NewToken(scope Scope, name string, entropy io.Reader) (Token, error) {
	name = strings.TrimSpace(name)
	if !scope.Valid() || name == "" || len(name) > 128 || strings.ContainsFunc(name, unicode.IsControl) {
		return Token{}, fmt.Errorf("令牌需要 read/config/admin 权限和不超过 128 字节的非空名称，名称不能包含控制字符")
	}
	if entropy == nil {
		return Token{}, fmt.Errorf("生成令牌需要安全随机源")
	}
	var secret [32]byte
	defer clear(secret[:])
	if _, err := io.ReadFull(entropy, secret[:]); err != nil {
		return Token{}, fmt.Errorf("生成令牌随机值失败：%w", err)
	}
	value := "lgmon_" + base64.RawURLEncoding.EncodeToString(secret[:])
	return Token{Value: value, Name: name, Scope: scope, Digest: TokenHash(value)}, nil
}
