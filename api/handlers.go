package api

import (
	"encoding/json"
	"fmt"
	"guptime/monitor"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/patrickmn/go-cache"
)

// APIHandler holds dependencies for the API handlers, such as the monitoring service and a cache.
type APIHandler struct {
	monitorService *monitor.Service
	cache          *cache.Cache
}

// NewAPIHandler creates a new handler with necessary dependencies.
func NewAPIHandler(service *monitor.Service) *APIHandler {
	// Initialize a new in-memory cache.
	// Cache items will expire after 60 seconds and the cache is cleaned up every 5 minutes.
	return &APIHandler{
		monitorService: service,
		cache:          cache.New(60*time.Second, 5*time.Minute),
	}
}

// RegisterRoutes sets up the API routes on the given chi router.
func (h *APIHandler) RegisterRoutes(r chi.Router) {
	r.Get("/monitors", h.getMonitors)
	r.Get("/monitors/{monitorID}/summary", h.getMonitorSummary)
	r.Get("/monitors/{monitorID}/checks", h.getMonitorChecks)
}

// getMonitors returns a list of all configured monitors.
// @Summary      List all monitors
// @Description  get a list of all monitors configured in the system
// @Tags         monitors
// @Accept       json
// @Produce      json
// @Success      200  {array}   monitor.Monitor
// @Router       /monitors [get]
func (h *APIHandler) getMonitors(w http.ResponseWriter, r *http.Request) {
	monitors := h.monitorService.GetMonitors()
	respondWithJSON(w, http.StatusOK, monitors)
}

// getMonitorSummary provides a high-level summary for a single monitor. It uses a cache
// to avoid hitting the database on every request.
// @Summary      Get a monitor summary
// @Description  get a high-level summary of a single monitor's performance over the last 24 hours
// @Tags         monitors
// @Accept       json
// @Produce      json
// @Param        monitorID path string true "Monitor ID"
// @Success      200  {object}  monitor.MonitorSummary
// @Failure      404  {object}  map[string]string
// @Router       /monitors/{monitorID}/summary [get]
func (h *APIHandler) getMonitorSummary(w http.ResponseWriter, r *http.Request) {
	monitorID := chi.URLParam(r, "monitorID")
	cacheKey := "summary_" + monitorID

	// Attempt to retrieve the summary from the cache first.
	if cachedSummary, found := h.cache.Get(cacheKey); found {
		respondWithJSON(w, http.StatusOK, cachedSummary)
		return
	}

	// If it's not in the cache, fetch it from the monitoring service.
	summary, err := h.monitorService.GetMonitorSummary(monitorID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Monitor not found or no data available")
		return
	}

	// Store the newly fetched summary in the cache for subsequent requests.
	h.cache.Set(cacheKey, summary, cache.DefaultExpiration)
	respondWithJSON(w, http.StatusOK, summary)
}

// getMonitorChecks returns detailed time-series data for a monitor within a specified range.
// @Summary      Get monitor checks
// @Description  get detailed time-series data for a monitor within a specified time range
// @Tags         monitors
// @Accept       json
// @Produce      json
// @Param        monitorID path string true "Monitor ID"
// @Param        range query string false "Time range preset (e.g., '1h', '24h', '7d', '30d'). Default is '24h'."
// @Param        start_time query int false "Start time as a Unix timestamp. Overrides 'range'."
// @Param        end_time query int false "End time as a Unix timestamp. Defaults to now."
// @Success      200  {array}   monitor.MonitorLogEntry
// @Failure      400  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Router       /monitors/{monitorID}/checks [get]
func (h *APIHandler) getMonitorChecks(w http.ResponseWriter, r *http.Request) {
	monitorID := chi.URLParam(r, "monitorID")

	// Parse time range parameters from the request URL.
	startTime, endTime, err := parseTimeRange(r)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, err.Error())
		return
	}

	// TODO: Implement data aggregation for large time ranges as per Phase 2 plan.
	checks, err := h.monitorService.GetMonitorChecks(monitorID, startTime, endTime)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Monitor not found or no data available for the given range")
		return
	}

	respondWithJSON(w, http.StatusOK, checks)
}

// parseTimeRange determines the start and end timestamps from URL query parameters.
// It supports presets like "1h", "24h", "7d", "30d" and custom "start_time" and "end_time".
func parseTimeRange(r *http.Request) (int64, int64, error) {
	now := time.Now()
	endTimeStr := r.URL.Query().Get("end_time")
	startTimeStr := r.URL.Query().Get("start_time")
	rangeStr := r.URL.Query().Get("range")

	var endTime int64
	if endTimeStr != "" {
		parsedEnd, err := strconv.ParseInt(endTimeStr, 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid end_time parameter")
		}
		endTime = parsedEnd
	} else {
		endTime = now.Unix()
	}

	// A specific start_time takes precedence over a time range preset.
	if startTimeStr != "" {
		parsedStart, err := strconv.ParseInt(startTimeStr, 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid start_time parameter")
		}
		if parsedStart > endTime {
			return 0, 0, fmt.Errorf("start_time cannot be after end_time")
		}
		return parsedStart, endTime, nil
	}

	// If no start_time, use a preset range or default to 24h.
	duration := 24 * time.Hour // Default
	if rangeStr != "" {
		var err error
		duration, err = time.ParseDuration(rangeStr)
		if err == nil { // e.g., "1h", "24h"
			// special cases for days
			if rangeStr == "7d" {
				duration = 7 * 24 * time.Hour
			} else if rangeStr == "30d" {
				duration = 30 * 24 * time.Hour
			}
		} else {
			return 0, 0, fmt.Errorf("invalid range parameter, use formats like '1h', '24h', '7d', '30d'")
		}
	}

	startTime := now.Add(-duration).Unix()
	return startTime, endTime, nil
}

// respondWithError is a helper function to send a JSON error message with a specific HTTP status code.
func respondWithError(w http.ResponseWriter, code int, message string) {
	log.Printf("API Error: status=%d, message=%s", code, message)
	respondWithJSON(w, code, map[string]string{"error": message})
}

// respondWithJSON is a helper function to marshal a payload to JSON and write the HTTP response.
func respondWithJSON(w http.ResponseWriter, code int, payload interface{}) {
	response, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Error marshalling JSON response: %v", err)
		// Fallback to a plain text error if JSON marshalling fails.
		http.Error(w, "Failed to marshal JSON response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(response)
}
