package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// AssistantWithout lists MCP tools 波西米亚 does not get. Adding a service needs someone who can look around the
// computer, so that stays with agents; put_config replaces whole files; the assistant's own keys never go through chat.
var AssistantWithout = []string{
	"start_here", "add_monitor", "test_target_match", "preview_service_switch_import", "import_service_switch",
	"put_config", "get_assistant", "configure_assistant",
}

// AssistantConfirm lists tools that delete, revoke, reach outside this computer or interrupt a service:
// the person approves each call in the chat before it runs.
var AssistantConfirm = []string{
	"remove_monitor", "delete_board", "delete_dashboard", "delete_template", "delete_silence",
	"lan_configure", "lan_approve", "lan_revoke", "restart_service", "delete_department",
	"create_team", "invite_team_member", "remove_team_member", "create_share", "update_share", "revoke_share",
	"join_team", "leave_team", "change_team_address", "test_notifier", "feishu_test_chat", "feishu_save_chat",
}

// assistantApprovalWait bounds how long a proposed step waits for the person before it counts as declined.
const assistantApprovalWait = 3 * time.Minute

// AssistantTool is an MCP tool as 波西米亚 is offered it.
type AssistantTool struct {
	Name, Description string
	Schema            map[string]any
	ReadOnly, Confirm bool
}

// AssistantToolbox runs MCP tools for one assistant turn with the asking page's own credentials.
type AssistantToolbox interface {
	Tools() []AssistantTool
	// Call returns the tool's JSON result, or its error, the way an MCP client would see it.
	Call(ctx context.Context, name string, arguments json.RawMessage) (output string, failed bool)
	// Approval registers a step the person must confirm; wait blocks until they answer, the time runs out or ctx ends.
	Approval() (id string, wait func(ctx context.Context) bool)
}

type assistantToolbox struct {
	server *Server
	origin *http.Request
	tools  []mcpTool
}

func (s *Server) assistantToolbox(origin *http.Request) (*assistantToolbox, error) {
	all, err := compiledMCPTools()
	if err != nil {
		return nil, err
	}
	box := &assistantToolbox{server: s, origin: origin}
	for _, tool := range all {
		if !slices.Contains(AssistantWithout, tool.binding.name) {
			box.tools = append(box.tools, tool)
		}
	}
	return box, nil
}

func (t *assistantToolbox) Tools() []AssistantTool {
	tools := make([]AssistantTool, 0, len(t.tools))
	for _, tool := range t.tools {
		tools = append(tools, AssistantTool{Name: tool.binding.name, Description: tool.binding.description, Schema: tool.schema,
			ReadOnly: tool.readOnly, Confirm: slices.Contains(AssistantConfirm, tool.binding.name)})
	}
	return tools
}

func (t *assistantToolbox) Call(ctx context.Context, name string, arguments json.RawMessage) (string, bool) {
	for _, tool := range t.tools {
		if tool.binding.name != name {
			continue
		}
		result := tool.run(ctx, MCPCallerFunc(func(ctx context.Context, op Operation, input Request) (MCPResponse, error) {
			return t.server.reenter(ctx, t.origin, "assistant", op, input)
		}), arguments)
		text := ""
		for _, content := range result.Content {
			if value, ok := content.(*mcp.TextContent); ok {
				text += value.Text
			}
		}
		return text, result.IsError
	}
	data, _ := json.Marshal(map[string]any{"error": map[string]string{"code": "unknown_tool", "message": "没有名为 " + name + " 的工具"}})
	return string(data), true
}

func (t *assistantToolbox) Approval() (string, func(context.Context) bool) {
	random := make([]byte, 16)
	_, _ = rand.Read(random)
	id := hex.EncodeToString(random)
	answer := make(chan bool, 1)
	t.server.approvals.Store(id, answer)
	return id, func(ctx context.Context) bool {
		defer t.server.approvals.Delete(id)
		timer := time.NewTimer(assistantApprovalWait)
		defer timer.Stop()
		select {
		case approved := <-answer:
			return approved
		case <-timer.C:
			return false
		case <-ctx.Done():
			return false
		}
	}
}

// AssistantDecision answers a step 波西米亚 proposed in the chat.
type AssistantDecision struct {
	ID      string `json:"id" jsonschema:"minLength=1,maxLength=64" jsonschema_description:"confirm 事件里的 id。"`
	Approve bool   `json:"approve" jsonschema_description:"true 执行，false 不执行。"`
}

func (s *Server) assistantDecide(w http.ResponseWriter, request Request) {
	var input AssistantDecision
	if err := DecodeBody(request.Body, &input); err != nil {
		s.failure(w, AssistantConfirmStep, err)
		return
	}
	value, ok := s.approvals.Load(input.ID)
	if !ok {
		writeError(w, 404, "approval_expired", "这一步已经过期或已经回答过了，请让波西米亚重新提出")
		return
	}
	select {
	case value.(chan bool) <- input.Approve:
		writeJSON(w, 200, Confirmation{OK: true, Message: "已收到"})
	default:
		writeError(w, 409, "approval_answered", "这一步已经回答过了")
	}
}
