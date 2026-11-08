# 通用 Webhook 与通知筛选

cert_expiry 的消息摘要含本地到期日；data 额外带可选 cert 对象：notAfter 为 UTC RFC3339 毫秒时间字符串，subject 为证书主体，daysLeft 为向上取整的剩余天数。其他类型省略 cert。触发/恢复使用各自事件持久快照，重启补发不借用后来续期的证书；version 仍为 1，原字段和签名协议不变。飞书 Webhook 卡片使用 [模板变量 .Cert](card-templates.md)。

通用渠道使用 `type: webhook`。地址与签名密钥经管理员的 `POST /api/v1/notifiers/{id}/credentials` 保存到密钥库；请求体及只显示一次的生成密钥见 [API](api.md)。也可用 `lgmon secret set` 从隐藏输入保存 URL 和密钥，再将名称填入 urlRef、secretRef。以下是虚构的引用示例，不含实际凭证：

```yaml
notifiers:
  - id: web-team
    type: webhook
    urlRef: web-team-url
    secretRef: web-team-sign
    filter:
      severity: [critical, warning]
      tags: [web, api]
      collectors: [web-01, WEB-02]
```

把 web-team 加入对应规则的 notify 后才接收告警。省略 secretRef 时不发送签名头。推荐 HTTPS；HTTP 会明文传输告警内容。URL 支持查询参数，其中的令牌同地址一起加密保存，不进入配置或日志。

## 请求与重试

使用 [Standard Webhooks 1.0](https://github.com/standard-webhooks/standard-webhooks/blob/main/spec/standard-webhooks.md) 的签名协议；本项目的字段与发送策略以 [PLAN M1-12](PLAN.md#webhook-协议与-filter) 为准。

- POST，Content-Type 为 application/json，User-Agent 为 `lg-monitor/<版本号>`；每次请求最多 15 秒。
- webhook-id 是告警 ID、状态、NotificationKey、渠道 ID 按既有飞书 UUID 的零字节分隔规则计算的 SHA-256 前 20 字节，写成 40 位十六进制。同事件重试和重启补发保持不变；测试事件每次随机生成。
- webhook-timestamp 是本次发送的 Unix 秒数，每次尝试重新取值。
- 签名密钥为 `whsec_` 加标准 Base64，解码后 24～64 字节。新建时不提供密钥，管理员接口生成 32 字节密钥并只显示一次；已有或本人提供的密钥不返回。
- webhook-signature 为 `v1,` 加 HMAC-SHA256 的标准 Base64。HMAC 密钥使用解码后的字节，签名原文是 `id.timestamp.body`；body 必须是收到的原始字节，不能先解析再重新序列化。M1 不发送用于密钥轮换的多签名。
- 仅 2xx 成功。3xx 不跟随，提示改成跳转后的地址；网络错误、超时、408、429、5xx 按 2、5、15 秒重试，最多四次，其余状态不重试。
- 响应正文最多读 4096 字节后丢弃，不写日志或接口；审计只记 HTTP 状态与中文原因。成功不生成提供方消息 ID。
- 每渠道有独立 FIFO 队列，一个接收方超时不拖住其他渠道。最近 24 小时未成功事件重启后按当前配置补发；接收方必须自行持久去重。

## 请求体

```json
{
  "type": "alert.firing",
  "timestamp": "2026-11-07T08:00:00.123Z",
  "data": {
    "version": 1,
    "alert": {
      "id": "fake-alert",
      "state": "firing",
      "severity": "critical",
      "summary": "示例服务停止",
      "detail": "没有匹配到进程",
      "value": "",
      "startedAt": "2026-11-07T08:00:00.123Z",
      "resolvedAt": null,
      "acknowledged": false,
      "flapping": false
    },
    "rule": {"id": "web-down", "name": "网站进程", "type": "process_absent"},
    "target": {"id": "web", "name": "网站", "tags": ["web"]},
    "collector": {"id": "web-01", "hostname": "WEB-01"},
    "dashboardUrl": null
  }
}
```

type 为 alert.firing、alert.renotify、alert.resolved 或 alert.test。重复提醒依据 NotificationKey 的 renotify: 前缀区分；测试摘要包含“这是测试消息”。抖动合并保持本次 type，flapping=true。

根 timestamp 是通知事件时间（NotificationAt），缺失时使用当前注入时钟；它与每次请求重新生成的头时间戳分开。所有时间使用 UTC RFC3339 毫秒；未知的 startedAt、resolvedAt、dashboardUrl 为 null，缺失字符串为空字符串。target.tags 来自发送时的配置，没有标签为 []；采集器失联 target.id 为 @collector，SQL 告警没有对应目标时同样无标签。未知 hostname 使用采集器 ID。

summary、detail、value 各最多 2000 个 Unicode 字符，截断时最后一个字符为“…”。不包含进程列表或日志尾部。Schema 为 [webhook-payload.schema.json](../schemas/webhook-payload.schema.json)，由 `lgmon config schema` 同步生成。version=1，增加字段不改版本，删除字段或改变含义时加版本；接收方须允许未知字段。

## 接收端示例

下面是完整的 Go 程序，只用标准库。将管理员首次返回的密钥放进 WEBHOOK_SECRET，保存为独立开发目录中的 receiver.go，执行 `go run receiver.go`，然后将渠道 URL 配成 `http://127.0.0.1:8099/hook`。不要把真实密钥写进文件或命令参数。

验签必须使用常量时间比较，拒绝与当前时间相差超过五分钟的请求，并按 webhook-id 去重。示例只在内存去重 25 小时；用于实际告警处理时，将“登记 ID 与处理事件”放在同一个持久事务中，进程重启后仍能去重。轮换密钥时同步更新两端。

```go
package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

func main() {
	secret := os.Getenv("WEBHOOK_SECRET")
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if err != nil || !strings.HasPrefix(secret, "whsec_") || len(key) < 24 || len(key) > 64 {
		log.Fatal("请在 WEBHOOK_SECRET 中设置有效的签名密钥")
	}
	defer clear(key)
	seen := map[string]time.Time{}
	var mu sync.Mutex
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/hook" {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			http.Error(w, "请求体过大或读取失败", http.StatusBadRequest)
			return
		}
		id, timestamp := r.Header.Get("webhook-id"), r.Header.Get("webhook-timestamp")
		seconds, err := strconv.ParseInt(timestamp, 10, 64)
		now := time.Now()
		if err != nil || id == "" || now.Sub(time.Unix(seconds, 0)).Abs() > 5*time.Minute {
			http.Error(w, "投递编号或时间戳无效", http.StatusUnauthorized)
			return
		}
		signature := r.Header.Get("webhook-signature")
		given, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(signature, "v1,"))
		mac := hmac.New(sha256.New, key)
		fmt.Fprintf(mac, "%s.%s.", id, timestamp)
		mac.Write(body)
		if err != nil || !strings.HasPrefix(signature, "v1,") || !hmac.Equal(mac.Sum(nil), given) {
			http.Error(w, "签名无效", http.StatusUnauthorized)
			return
		}
		var event struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(body, &event); err != nil {
			http.Error(w, "JSON 无效", http.StatusBadRequest)
			return
		}
		mu.Lock()
		for previous, at := range seen {
			if now.Sub(at) > 25*time.Hour {
				delete(seen, previous)
			}
		}
		_, duplicate := seen[id]
		if !duplicate {
			seen[id] = now
			log.Printf("收到事件：type=%s id=%s", event.Type, id)
		}
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	})
	server := &http.Server{Addr: "127.0.0.1:8099", Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second}
	log.Fatal(server.ListenAndServe())
}
```

## filter

所有渠道类型均支持 severity、tags、collectors 三个可选列表。一个字段内为“或”，不同字段间为“且”。省略字段表示该项不限；省略 filter 或使用空对象表示全部接收；显式 [] 或 null 校验失败，不限制就不要写这一项。

- severity 只接受 critical、warning、info，使用告警记录的严重程度。
- tags 与目标当前标签精确匹配，区分大小写。采集器失联、没有对应目标的 SQL 告警及其他无标签告警均不匹配 tags 筛选。
- collectors 与告警记录的采集器 ID 精确匹配，或与已知主机名不区分大小写匹配，不支持 `*`。
- 规则 notify 先决定候选渠道，filter 只能缩小范围。默认生成的失联规则以全部渠道为候选，也经过 filter。
- 每次发送前重新筛选，触发、重复提醒和恢复分别判断。中间修改标签或 filter 后，恢复通知的去向会随之改变。
- 被筛掉不写失败审计、不重试；全部筛掉只记一条 info 日志。重启补发按当前配置判断。测试接口绕过 filter。
- 显式规则 notify 引用的渠道如果 severity 永远不接收该规则，配置校验报错；默认生成的失联规则不做这项冲突检查。

## 事件诊断

使用引用 Events 的模板时，通用 Webhook 的 data 可带 events 数组，最多五项，字段为 time、provider、eventId、subject、message；time 为 UTC RFC3339 毫秒，message 最多 200 个 Unicode 字符。只发送当时取得的事件，不更新已发送请求。原有字段、version=1 和 Standard Webhooks 签名协议不变；完整窗口通过只读 GET alerts/{id}/events 或 MCP get_alert_events 分页读取。
