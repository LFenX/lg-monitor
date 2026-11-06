package config

import (
	"fmt"
	"strings"
)

func ValidateRuleSQL(query string) error {
	if err := ValidateReadOnlySQL(query); err != nil {
		return err
	}
	tokens, _ := SQLTokens(query)
	for i, token := range tokens {
		name := strings.ToLower(token.Text)
		if token.Kind == "literal" && strings.EqualFold(token.Text, "now") {
			return fmt.Errorf("SQL 规则不能读取系统时钟；把 datetime('now','-5 minutes') 改成 :now - 300000")
		}
		if token.Kind == "word" || token.Kind == "identifier" || token.Kind == "literal" {
			if token.Kind != "literal" && oneOf(name, "current_timestamp", "current_date", "current_time") {
				return fmt.Errorf("SQL 规则不能使用 CURRENT_TIMESTAMP/DATE/TIME，请使用 :now UTC 毫秒")
			}
			if oneOf(name, "main", "temp") && i+1 < len(tokens) && tokens[i+1].Text == "." {
				return fmt.Errorf("SQL 规则不能指定 main. 或 temp. 库名，请直接使用表名，让引擎按 :now 截断")
			}
			if oneOf(name, "date", "time", "datetime", "julianday", "unixepoch", "strftime") && i+1 < len(tokens) && tokens[i+1].Text == "(" {
				depth, count, hasValue := 0, 0, false
				for j := i + 2; j < len(tokens); j++ {
					text := tokens[j].Text
					if tokens[j].Kind == "punctuation" {
						if text == ")" && depth == 0 {
							if hasValue {
								count++
							}
							break
						}
						if text == "," && depth == 0 {
							count++
							hasValue = false
							continue
						}
						if text == "(" {
							depth++
						}
						if text == ")" {
							depth--
						}
					}
					hasValue = true
				}
				minimum := 1
				if name == "strftime" {
					minimum = 2
				}
				if count < minimum {
					return fmt.Errorf("SQL 日期函数缺少时间参数，请使用 :now 或 datetime(ts/1000,'unixepoch')")
				}
			}
		}
	}
	return nil
}
