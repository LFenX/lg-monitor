package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type LiveEvent struct {
	ID   uint64
	Name string
	Data json.RawMessage
}

type Broker struct {
	mu      sync.Mutex
	next    uint64
	closed  bool
	clients map[chan LiveEvent]bool
}

func NewBroker() *Broker { return &Broker{clients: map[chan LiveEvent]bool{}} }

func (b *Broker) Publish(name string, value any) error {
	if name != "target" && name != "alert" && name != "sample" && name != "dashboard" {
		return fmt.Errorf("实时事件类型必须为 target、alert、sample 或 dashboard")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("编码实时事件失败：%w", err)
	}
	if len(data) > MaxRequestBody {
		return fmt.Errorf("实时事件超过 1 MiB")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return fmt.Errorf("实时事件服务已关闭")
	}
	b.next++
	event := LiveEvent{b.next, name, data}
	for client := range b.clients {
		select {
		case client <- event:
		default:
			delete(b.clients, client)
			close(client)
		}
	}
	return nil
}

func (b *Broker) subscribe() (<-chan LiveEvent, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	client := make(chan LiveEvent, 64)
	if b.closed {
		close(client)
	} else {
		b.clients[client] = true
	}
	return client, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.clients[client] {
			delete(b.clients, client)
			close(client)
		}
	}
}

func (b *Broker) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	for client := range b.clients {
		delete(b.clients, client)
		close(client)
	}
}

type StreamTicker interface {
	C() <-chan time.Time
	Stop()
}
type StreamClock interface {
	NewTicker(time.Duration) StreamTicker
}
type nativeStreamClock struct{}
type nativeStreamTicker struct{ *time.Ticker }

func (nativeStreamClock) NewTicker(d time.Duration) StreamTicker {
	return nativeStreamTicker{time.NewTicker(d)}
}
func (t nativeStreamTicker) C() <-chan time.Time { return t.Ticker.C }

func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, 500, "stream_unavailable", "HTTP 连接不支持实时推送")
		return
	}
	events, unsubscribe := s.options.Broker.subscribe()
	defer unsubscribe()
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Cache-Control", "no-store")
	ticker := s.options.StreamClock.NewTicker(15 * time.Second)
	defer ticker.Stop()
	send := func(value string) bool {
		// A slow reader must release its handler instead of holding shutdown open.
		_ = http.NewResponseController(w).SetWriteDeadline(s.options.Now().Add(10 * time.Second))
		if _, err := fmt.Fprint(w, value); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	if !send(": connected\n\n") {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C():
			if s.options.Auth.CheckHost(r) != nil {
				return
			}
			_, found, err := s.options.Auth.authenticate(r)
			if err != nil || !found || !send(": heartbeat\n\n") {
				return
			}
		case event, ok := <-events:
			if !ok {
				send("event: reset\ndata: {\"message\":\"请重新读取当前快照\"}\n\n")
				return
			}
			if !send("id: " + strconv.FormatUint(event.ID, 10) + "\nevent: " + event.Name + "\ndata: " + string(event.Data) + "\n\n") {
				return
			}
		}
	}
}
