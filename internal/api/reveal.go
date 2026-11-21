// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// RevealPages asks pages already open on this computer to go to a page and refresh, so lgmon's window and tray
// reuse the browser tab people already have instead of opening another one each time.
const RevealPages Operation = "reveal_pages"

// RevealInput is what lgmon's window or tray sends when someone opens the board.
type RevealInput struct {
	Reuse bool   `json:"reuse,omitempty" jsonschema_description:"本机启动器请求复用一个面板；已有页面或正在打开时返回 reused=true。"`
	Hash  string `json:"hash,omitempty" jsonschema:"maxLength=300" jsonschema_description:"要打开的页面，网页地址 # 之后的部分，例如 #/boards/main；省略表示只刷新。"`
	Nonce string `json:"nonce,omitempty" jsonschema:"pattern=^[0-9a-f]{4}$" jsonschema_description:"四位十六进制标记：页面把它以不可见字符加在标题末尾几秒，窗口据此找到这个浏览器窗口并带到前面。"`
}

type RevealResult struct {
	Confirmation
	Reused bool `json:"reused"`
}

// RevealBoard chooses one local board. While the default browser starts, subsequent
// clicks update its pending destination instead of starting more browser windows.
func (b *Broker) RevealBoard(input RevealInput, now time.Time) (bool, error) {
	data, err := json.Marshal(input)
	if err != nil {
		return false, fmt.Errorf("编码面板地址失败：%w", err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return false, fmt.Errorf("实时事件服务已关闭")
	}
	b.next++
	event := LiveEvent{b.next, "reveal", data}
	for {
		if !b.clients[b.board] {
			b.board = nil
			for client, board := range b.clients {
				if board {
					b.board = client
					break
				}
			}
		}
		if b.board == nil {
			break
		}
		select {
		case b.board <- event:
			return true, nil
		default:
			delete(b.clients, b.board)
			close(b.board)
		}
	}
	if now.Before(b.launchUntil) {
		b.pending = event
		return true, nil
	}
	b.launchUntil, b.pending = now.Add(8*time.Second), LiveEvent{}
	return false, nil
}

var revealNonce = regexp.MustCompile(`^[0-9a-f]{4}$`)

func revealRoutes() []Route {
	return []Route{
		{RevealPages, "POST", "/api/v1/pages/reveal", "让这台电脑上已经打开的 lgmon 页面跳到指定页面并刷新（小窗口和托盘“打开看板”用）", ScopeRead, RevealInput{}, RevealResult{}, nil},
	}
}

func (s *Server) reveal(w http.ResponseWriter, request Request, local bool) {
	var input RevealInput
	if err := DecodeBody(request.Body, &input); err != nil {
		s.failure(w, RevealPages, err)
		return
	}
	if input.Nonce != "" && !revealNonce.MatchString(input.Nonce) || !validRevealHash(input.Hash) {
		s.failure(w, RevealPages, &Fault{Status: 400, Code: "invalid_request", Message: "hash 必须以 #/ 开头且不超过 300 个字符，nonce 是四位十六进制"})
		return
	}
	result := RevealResult{Confirmation: Confirmation{OK: true, Message: "已通知打开的页面"}}
	var err error
	if input.Reuse {
		if !local {
			writeError(w, 403, "local_only", "请从监控器所在电脑打开面板")
			return
		}
		result.Reused, err = s.options.Broker.RevealBoard(input, s.options.Now())
	} else {
		err = s.options.Broker.Publish("reveal", input)
	}
	if err != nil {
		s.failure(w, RevealPages, err)
		return
	}
	writeJSON(w, 200, result)
}

func validRevealHash(hash string) bool {
	if hash == "" {
		return true
	}
	return strings.HasPrefix(hash, "#/") && len(hash) <= 300 && !strings.ContainsFunc(hash, unicode.IsControl)
}
