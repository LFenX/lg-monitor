package main

import (
	"encoding/json"
	"log/slog"

	"github.com/spf13/cobra"
)

type templateValidationResult struct {
	ID    string `json:"id"`
	Valid bool   `json:"valid"`
	Error string `json:"error,omitempty"`
}

func commandJSON(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	value, _ := cmd.Flags().GetBool("json")
	return value
}

func commandLogger(cmd *cobra.Command) *slog.Logger {
	if !commandJSON(cmd) {
		return slog.Default()
	}
	var level slog.Level
	if value, err := cmd.Flags().GetString("log-level"); err == nil {
		_ = level.UnmarshalText([]byte(value))
	}
	return slog.New(slog.NewJSONHandler(cmd.ErrOrStderr(), &slog.HandlerOptions{Level: level}))
}

func commandEvent(cmd *cobra.Command, event, message string) error {
	return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
		Event   string `json:"event"`
		Message string `json:"message,omitempty"`
	}{event, message})
}
