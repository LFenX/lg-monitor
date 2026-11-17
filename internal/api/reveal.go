package api

import (
	"net/http"
	"regexp"
	"strings"
	"unicode"
)

// RevealPages asks pages already open on this computer to go to a page and refresh, so lgmon's window and tray
// reuse the browser tab people already have instead of opening another one each time.
const RevealPages Operation = "reveal_pages"

// RevealInput is what lgmon's window or tray sends when someone opens the board.
type RevealInput struct {
	Hash  string `json:"hash,omitempty" jsonschema:"maxLength=300" jsonschema_description:"要打开的页面，网页地址 # 之后的部分，例如 #/boards/main；省略表示只刷新。"`
	Nonce string `json:"nonce,omitempty" jsonschema:"pattern=^[0-9a-f]{4}$" jsonschema_description:"四位十六进制标记：页面把它以不可见字符加在标题末尾几秒，窗口据此找到这个浏览器窗口并带到前面。"`
}

var revealNonce = regexp.MustCompile(`^[0-9a-f]{4}$`)

func revealRoutes() []Route {
	return []Route{
		{RevealPages, "POST", "/api/v1/pages/reveal", "让这台电脑上已经打开的 lgmon 页面跳到指定页面并刷新（小窗口和托盘“打开看板”用），不新开页面", ScopeRead, RevealInput{}, Confirmation{}, nil},
	}
}

func (s *Server) reveal(w http.ResponseWriter, request Request) {
	var input RevealInput
	if err := DecodeBody(request.Body, &input); err != nil {
		s.failure(w, RevealPages, err)
		return
	}
	if input.Nonce != "" && !revealNonce.MatchString(input.Nonce) || !validRevealHash(input.Hash) {
		s.failure(w, RevealPages, &Fault{Status: 400, Code: "invalid_request", Message: "hash 必须以 #/ 开头且不超过 300 个字符，nonce 是四位十六进制"})
		return
	}
	if err := s.options.Broker.Publish("reveal", input); err != nil {
		s.failure(w, RevealPages, err)
		return
	}
	writeJSON(w, 200, Confirmation{OK: true, Message: "已通知打开的页面"})
}

func validRevealHash(hash string) bool {
	if hash == "" {
		return true
	}
	return strings.HasPrefix(hash, "#/") && len(hash) <= 300 && !strings.ContainsFunc(hash, unicode.IsControl)
}
