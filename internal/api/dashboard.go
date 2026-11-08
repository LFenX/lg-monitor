package api

import (
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/rules"
)

type DashboardSummary struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	File       string  `json:"file"`
	Version    string  `json:"version"`
	PanelCount int     `json:"panelCount"`
	Error      *string `json:"error" jsonschema:"nullable"`
	HTML       bool    `json:"html,omitempty"`
}
type DashboardPanel struct {
	config.DashboardPanel
	Parameters []string `json:"parameters"`
	Error      *string  `json:"error" jsonschema:"nullable"`
}
type DashboardDefinition struct {
	ID           string           `json:"id"`
	Title        string           `json:"title"`
	Refresh      config.Duration  `json:"refresh"`
	DefaultRange config.Duration  `json:"defaultRange"`
	File         string           `json:"file"`
	Version      string           `json:"version"`
	Error        *string          `json:"error" jsonschema:"nullable"`
	Panels       []DashboardPanel `json:"panels"`
	HTML         *DashboardHTML   `json:"html,omitempty"`
}

type DashboardHTML struct {
	File   string `json:"file"`
	Source string `json:"source"`
}
type QueryResult struct {
	rules.QueryResult
	Params config.QueryValues `json:"params"`
}
type PanelData struct {
	QueryResult
	Version string `json:"version"`
}
