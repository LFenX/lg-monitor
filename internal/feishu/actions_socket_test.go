package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/gorilla/websocket"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

type actionBootstrapClient struct{ endpoint string }

func (c actionBootstrapClient) Do(request *http.Request) (*http.Response, error) {
	if request.URL.Host != "open.feishu.cn" || !strings.Contains(request.URL.Path, "endpoint") {
		return nil, fmt.Errorf("unexpected bootstrap destination")
	}
	var credentials larkws.BootstrapRequest
	if err := json.NewDecoder(request.Body).Decode(&credentials); err != nil {
		return nil, err
	}
	if credentials.AppID != "cli_fake_app" || credentials.AppSecret != "fake-ws-secret" {
		return nil, fmt.Errorf("wrong bootstrap credential")
	}
	body, _ := json.Marshal(larkws.EndpointResp{Code: 0, Data: &larkws.Endpoint{Url: c.endpoint, ClientConfig: &larkws.ClientConfig{ReconnectCount: 0, ReconnectInterval: 1, PingInterval: 3600}}})
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
}

func TestActionStreamOfficialSDKLocalWebSocketRoundTrip(t *testing.T) {
	receipt := make(chan []byte, 1)
	errors := make(chan error, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		socket, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			errors <- err
			return
		}
		defer socket.Close()
		_ = socket.SetReadDeadline(time.Now().Add(5 * time.Second))
		payload, _ := json.Marshal(actionSDKPayload())
		headers := larkws.Headers{}
		headers.Add(larkws.HeaderType, string(larkws.MessageTypeEvent))
		headers.Add(larkws.HeaderMessageID, "fake-wire-message")
		headers.Add(larkws.HeaderSum, "1")
		headers.Add(larkws.HeaderSeq, "0")
		data, err := (&larkws.Frame{Method: int32(larkws.FrameTypeData), Headers: headers, Payload: payload}).Marshal()
		if err != nil {
			errors <- err
			return
		}
		if err := socket.WriteMessage(websocket.BinaryMessage, data); err != nil {
			errors <- err
			return
		}
		for {
			_, data, err := socket.ReadMessage()
			if err != nil {
				errors <- err
				return
			}
			var frame larkws.Frame
			if err := frame.Unmarshal(data); err != nil {
				errors <- err
				return
			}
			if frame.Method == int32(larkws.FrameTypeData) {
				receipt <- frame.Payload
				_, _, _ = socket.ReadMessage()
				return
			}
		}
	}))
	defer server.Close()
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http") + "/callback?device_id=fake-device&service_id=1"
	stream, err := NewActionStream(config.FeishuApp{AppID: "cli_fake_app", Domain: "feishu", Proxy: "none"}, []byte("fake-ws-secret"), ActionOptions{HTTPClient: actionBootstrapClient{endpoint: endpoint}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- stream.Run(ctx, func(ctx context.Context, event CardAction) CardActionReply {
			if event.MessageID != "om_fake_card" || event.OperatorID != "ou_fake_operator" {
				return CardActionReply{Type: "error", Message: "范围错误"}
			}
			return CardActionReply{Type: "success", Message: "夹具已确认"}
		}, nil)
	}()
	select {
	case data := <-receipt:
		var response larkws.Response
		if err := json.Unmarshal(data, &response); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 200 || !strings.Contains(string(response.Data), "夹具已确认") {
			t.Fatal("wrong callback wire response", string(data))
		}
	case err := <-errors:
		t.Fatal(err)
	case <-time.After(7 * time.Second):
		t.Fatal("official SDK did not reply to local WebSocket event")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("official SDK did not stop")
	}
}
