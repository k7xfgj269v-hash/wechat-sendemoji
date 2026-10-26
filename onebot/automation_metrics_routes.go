package main

import (
	"net/http"
	"time"
)

func registerAutomationMetricsRoutes(
	mux *http.ServeMux,
	metrics OperationalMetrics,
	operatorToken string,
) {
	if mux == nil {
		return
	}
	mux.HandleFunc(
		"/api/system/metrics",
		automationOperatorAuth(operatorToken, automationMetricsHandler(metrics)),
	)
}

func automationMetricsHandler(metrics OperationalMetrics) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			automationWriteMethodNotAllowed(w, http.MethodGet)
			return
		}
		observeHRWorkbenchAgentConnectivity(r.Context(), metrics)
		if metrics == nil {
			automationWriteJSON(w, http.StatusServiceUnavailable, emptyOperationalMetricsSnapshot(time.Now()))
			return
		}
		automationWriteJSON(w, http.StatusOK, metrics.Snapshot(time.Now()))
	}
}

func NewAutomationMetricsHandler(metrics OperationalMetrics) http.Handler {
	return automationMetricsHandler(metrics)
}
