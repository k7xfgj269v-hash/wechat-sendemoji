package main

import (
	"context"
	"net/http"
	"os"
	"time"
)

const systemRuntimeHealthTimeout = 2 * time.Second
const systemRuntimeProcess = "hr-workbench"

type SystemRuntimeHealthSource interface {
	Health(context.Context) (AgentHealth, error)
}

type SystemRuntimeHealth struct {
	Status         string       `json:"status"`
	Ready          bool         `json:"ready"`
	Process        string       `json:"process"`
	PID            int          `json:"pid"`
	StartedAt      string       `json:"started_at"`
	AgentConnected bool         `json:"agent_connected"`
	Agent          *AgentHealth `json:"agent,omitempty"`
}

func registerSystemRuntimeRoutes(
	mux *http.ServeMux,
	source SystemRuntimeHealthSource,
	operatorToken string,
) {
	startedAt := time.Now().UTC()
	mux.HandleFunc(
		"/api/system/health",
		automationOperatorAuth(
			operatorToken,
			systemRuntimeHealthHandler(source, startedAt, os.Getpid()),
		),
	)
}

func systemRuntimeHealthHandler(
	source SystemRuntimeHealthSource,
	startedAt time.Time,
	pid int,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			automationWriteMethodNotAllowed(w, http.MethodGet)
			return
		}

		response := SystemRuntimeHealth{
			Status:    "degraded",
			Process:   systemRuntimeProcess,
			PID:       pid,
			StartedAt: startedAt.UTC().Format(time.RFC3339),
		}
		status := http.StatusServiceUnavailable
		if source != nil {
			ctx, cancel := context.WithTimeout(r.Context(), systemRuntimeHealthTimeout)
			health, err := source.Health(ctx)
			cancel()
			if err == nil {
				response.AgentConnected = true
				response.Agent = &health
				if health.Ready {
					response.Status = "ok"
					response.Ready = true
					status = http.StatusOK
				}
			}
		}
		automationWriteJSON(w, status, response)
	}
}
