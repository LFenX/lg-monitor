package config

import (
	"fmt"
	"regexp"
	"strings"
)

var metricPattern = regexp.MustCompile("^[A-Za-z_][A-Za-z0-9_.-]{0,127}$")

type SQLToken struct {
	Kind, Text string
	Start, End int
}

func SQLTokens(query string) ([]SQLToken, error) {
	if strings.ContainsRune(query, 0) || len(query) > 256*1024 {
		return nil, fmt.Errorf("只读 SQL 不能含 NUL 或超过 256 KiB")
	}
	var tokens []SQLToken
	word := func(c byte) bool {
		return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c >= 128
	}
	for i := 0; i < len(query); {
		c, start := query[i], i
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			i++
			continue
		}
		if c == '-' && i+1 < len(query) && query[i+1] == '-' {
			i += 2
			for i < len(query) && query[i] != '\n' {
				i++
			}
			continue
		}
		if c == '/' && i+1 < len(query) && query[i+1] == '*' {
			end := strings.Index(query[i+2:], "*/")
			if end < 0 {
				return nil, fmt.Errorf("SQL 块注释未闭合")
			}
			i += end + 4
			continue
		}
		if c == '\'' || c == '"' || c == 96 || c == '[' {
			end := c
			if c == '[' {
				end = ']'
			}
			kind := "identifier"
			if c == '\'' {
				kind = "literal"
			}
			i++
			var value strings.Builder
			closed := false
			for i < len(query) {
				if query[i] == end {
					i++
					if end != ']' && i < len(query) && query[i] == end {
						value.WriteByte(end)
						i++
						continue
					}
					closed = true
					break
				}
				value.WriteByte(query[i])
				i++
			}
			if !closed {
				return nil, fmt.Errorf("SQL 字符串或标识符未闭合")
			}
			tokens = append(tokens, SQLToken{Kind: kind, Text: value.String(), Start: start, End: i})
			continue
		}
		if c == ':' || c == '@' || c == '$' || c == '?' {
			i++
			for i < len(query) && word(query[i]) {
				i++
			}
			tokens = append(tokens, SQLToken{Kind: "parameter", Text: query[start+1 : i], Start: start, End: i})
			continue
		}
		if word(c) {
			i++
			for i < len(query) && word(query[i]) {
				i++
			}
			tokens = append(tokens, SQLToken{Kind: "word", Text: query[start:i], Start: start, End: i})
			continue
		}
		i++
		tokens = append(tokens, SQLToken{Kind: "punctuation", Text: query[start:i], Start: start, End: i})
	}
	return tokens, nil
}

func ValidateReadOnlySQL(query string) error {
	tokens, err := SQLTokens(query)
	if err != nil {
		return err
	}
	if len(tokens) == 0 || tokens[0].Kind != "word" || !oneOf(strings.ToUpper(tokens[0].Text), "SELECT", "WITH") {
		return fmt.Errorf("只读 SQL 必须为非空的单条 SELECT 或 WITH 语句")
	}
	for i, token := range tokens {
		if token.Kind == "punctuation" && token.Text == ";" && i != len(tokens)-1 {
			return fmt.Errorf("只读 SQL 只接受一条语句")
		}
		if i+1 < len(tokens) && tokens[i+1].Text == "(" && oneOf(strings.ToLower(token.Text), "load_extension", "readfile", "writefile") {
			return fmt.Errorf("只读 SQL 不接受扩展加载或文件访问函数")
		}
	}
	return nil
}
