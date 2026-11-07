package config

import "github.com/invopop/jsonschema"

type WebhookTimestamp string

func (WebhookTimestamp) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "string", Format: "date-time", Pattern: `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`}
}

type WebhookPayload struct {
	Type      string           `json:"type" jsonschema:"enum=alert.firing,enum=alert.renotify,enum=alert.resolved,enum=alert.test"`
	Timestamp WebhookTimestamp `json:"timestamp"`
	Data      WebhookData      `json:"data"`
}

type WebhookData struct {
	Version      int              `json:"version" jsonschema:"enum=1"`
	Alert        WebhookAlert     `json:"alert"`
	Rule         WebhookRule      `json:"rule"`
	Target       WebhookTarget    `json:"target"`
	Collector    WebhookCollector `json:"collector"`
	DashboardURL *string          `json:"dashboardUrl" jsonschema:"nullable"`
}

type WebhookAlert struct {
	ID           string            `json:"id"`
	State        string            `json:"state" jsonschema:"enum=firing,enum=resolved"`
	Severity     string            `json:"severity" jsonschema:"enum=critical,enum=warning,enum=info"`
	Summary      string            `json:"summary" jsonschema:"maxLength=2000"`
	Detail       string            `json:"detail" jsonschema:"maxLength=2000"`
	Value        string            `json:"value" jsonschema:"maxLength=2000"`
	StartedAt    *WebhookTimestamp `json:"startedAt" jsonschema:"nullable"`
	ResolvedAt   *WebhookTimestamp `json:"resolvedAt" jsonschema:"nullable"`
	Acknowledged bool              `json:"acknowledged"`
	Flapping     bool              `json:"flapping"`
}

type WebhookRule struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

type WebhookTarget struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

type WebhookCollector struct {
	ID       string `json:"id"`
	Hostname string `json:"hostname"`
}
