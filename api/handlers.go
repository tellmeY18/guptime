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

	// Slug-based endpoints
	r.Get("/monitors/slug/{slug}", h.getMonitorsBySlug)
	r.Get("/monitors/slug/{slug}/summary", h.getSlugSummary)
	r.Get("/monitors/slug/{slug}/history", h.getSlugHistory) // NEW: 90 days status history
	r.Get("/monitors/slug/{slug}/checks", h.getSlugChecks)

	// Monitor endpoints now use both slug and name
	r.Get("/monitors/{slug}/{name}/summary", h.getMonitorSummary)
	r.Get("/monitors/{slug}/{name}/checks", h.getMonitorChecks)
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

// getMonitorSummary provides a high-level summary for a single monitor (by slug and name).
// @Summary      Get a monitor summary
// @Description  get a high-level summary of a single monitor's performance over the last 24 hours
// @Tags         monitors
// @Accept       json
// @Produce      json
// @Param        slug path string true "Slug"
// @Param        name path string true "Monitor Name"
// @Success      200  {object}  monitor.MonitorSummary
// @Failure      404  {object}  map[string]string
// @Router       /monitors/{slug}/{name}/summary [get]
func (h *APIHandler) getMonitorSummary(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	name := chi.URLParam(r, "name")
	cacheKey := "summary_" + slug + "_" + name

	// Attempt to retrieve the summary from the cache first.
	if cachedSummary, found := h.cache.Get(cacheKey); found {
		respondWithJSON(w, http.StatusOK, cachedSummary)
		return
	}

	// If it's not in the cache, fetch it from the monitoring service.
	summary, err := h.monitorService.GetMonitorSummary(slug, name)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Monitor not found or no data available")
		return
	}

	// Store the newly fetched summary in the cache for subsequent requests.
	h.cache.Set(cacheKey, summary, cache.DefaultExpiration)
	respondWithJSON(w, http.StatusOK, summary)
}

// getMonitorsBySlug returns all monitors for a given slug, including their summary (uptime data).
// @Summary      List monitors by slug
// @Description  get a list of all monitors for a given slug, including uptime summary
// @Tags         monitors
// @Accept       json
// @Produce      json
// @Param        slug path string true "Slug"
// @Success      200  {array}   object
// @Failure      404  {object}  map[string]string
// @Router       /monitors/slug/{slug} [get]
func (h *APIHandler) getMonitorsBySlug(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	monitors, err := h.monitorService.GetMonitorsBySlug(slug)
	if err != nil {
		respondWithError(w, http.StatusNotFound, err.Error())
		return
	}
	type monitorWithSummary struct {
		Name    string                      `json:"name"`
		Slug    string                      `json:"slug"`
		URL     string                      `json:"url"`
		Summary *monitor.MonitorSummary     `json:"summary"`
	}
	var result []monitorWithSummary
	for _, m := range monitors {
		summary, _ := h.monitorService.GetMonitorSummary(m.Slug, m.Name)
		result = append(result, monitorWithSummary{
			Name:    m.Name,
			Slug:    m.Slug,
			URL:     m.URL,
			Summary: summary,
		})
	}
	respondWithJSON(w, http.StatusOK, result)
}

// getSlugSummary returns summaries for all monitors under a slug.
// @Summary      Get summaries for all monitors under a slug
// @Description  get summaries for all monitors under a slug
// @Tags         monitors
// @Accept       json
// @Produce      json
// @Param        slug path string true "Slug"
// @Success      200  {array}   monitor.MonitorSummary
// @Failure      404  {object}  map[string]string
// @Router       /monitors/slug/{slug}/summary [get]
func (h *APIHandler) getSlugSummary(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	monitors, err := h.monitorService.GetMonitorsBySlug(slug)
	if err != nil {
		respondWithError(w, http.StatusNotFound, err.Error())
		return
	}
	summaries := make([]struct {
		monitor.Monitor
		Summary *monitor.MonitorSummary `json:"summary"`
	}, 0, len(monitors))
	for _, m := range monitors {
		summary, err := h.monitorService.GetMonitorSummary(m.Slug, m.Name)
		if err != nil {
			continue
		}
		summaries = append(summaries, struct {
			monitor.Monitor
			Summary *monitor.MonitorSummary `json:"summary"`
		}{
			Monitor: m,
			Summary: summary,
		})
	}
	respondWithJSON(w, http.StatusOK, summaries)
}

// getSlugHistory returns a 90-day status history for all monitors under a slug.
// @Summary      Get 90-day status history for all monitors under a slug
// @Description  Get a rich 90-day status history for all monitors under a slug, including daily uptime percentages and status breakdowns.
// @Tags         monitors
// @Accept       json
// @Produce      json
// @Param        slug path string true "Slug"
// @Success      200  {object}  map[string][]SlugMonitorDailyHistory
// @Failure      404  {object}  map[string]string
// @Router       /monitors/slug/{slug}/history [get]
func (h *APIHandler) getSlugHistory(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	monitors, err := h.monitorService.GetMonitorsBySlug(slug)
	if err != nil {
		respondWithError(w, http.StatusNotFound, err.Error())
		return
	}

	// Helper to interpret MonitorLogEntry.Response as a status string.
	getStatus := func(response string) string {
		if len(response) == 0 {
			return "unknown"
		}
		if response[0] == '2' {
			return "up"
		}
		if response[0] == '4' || response[0] == '5' {
			return "down"
		}
		if len(response) >= 5 && response[:5] == "Error" {
			return "down"
		}
		return "unknown"
	}

	type SlugMonitorDailyHistory struct {
		Date           string  `json:"date"`            // YYYY-MM-DD
		UptimePercent  float64 `json:"uptime_percent"`  // e.g. 99.99
		TotalChecks    int     `json:"total_checks"`
		UpChecks       int     `json:"up_checks"`
		DownChecks     int     `json:"down_checks"`
		UnknownChecks  int     `json:"unknown_checks"`
	}

	const days = 90
	now := time.Now()
	historyResult := make(map[string][]SlugMonitorDailyHistory)

	for _, m := range monitors {
		var dailyHistory []SlugMonitorDailyHistory
		for i := 0; i < days; i++ {
			dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -i)
			dayEnd := dayStart.Add(24 * time.Hour)
			startUnix := dayStart.Unix()
			endUnix := dayEnd.Unix()

			checks, err := h.monitorService.GetMonitorChecks(m.Slug, m.Name, startUnix, endUnix)
			if err != nil || len(checks) == 0 {
				// If no data, still add the day with zeros
				dailyHistory = append(dailyHistory, SlugMonitorDailyHistory{
					Date:          dayStart.Format("2006-01-02"),
					UptimePercent: 0,
					TotalChecks:   0,
					UpChecks:      0,
					DownChecks:    0,
					UnknownChecks: 0,
				})
				continue
			}

			var up, down, unknown int
			for _, c := range checks {
				switch getStatus(c.Response) {
				case "up":
					up++
				case "down":
					down++
				default:
					unknown++
				}
			}
			total := up + down + unknown
			uptimePercent := 0.0
			if total > 0 {
				uptimePercent = float64(up) / float64(total) * 100
			}
			dailyHistory = append(dailyHistory, SlugMonitorDailyHistory{
				Date:          dayStart.Format("2006-01-02"),
				UptimePercent: uptimePercent,
				TotalChecks:   total,
				UpChecks:      up,
				DownChecks:    down,
				UnknownChecks: unknown,
			})
		}
		// Reverse dailyHistory so oldest day is first
		for i, j := 0, len(dailyHistory)-1; i < j; i, j = i+1, j-1 {
			dailyHistory[i], dailyHistory[j] = dailyHistory[j], dailyHistory[i]
		}
		historyResult[m.Name] = dailyHistory
	}
	respondWithJSON(w, http.StatusOK, historyResult)
}

// getSlugChecks returns all checks for all monitors under a slug (optionally with time range).
// @Summary      Get all checks for all monitors under a slug
// @Description  get all checks for all monitors under a slug
// @Tags         monitors
// @Accept       json
// @Produce      json
// @Param        slug path string true "Slug"
// @Param        range query string false "Time range preset (e.g., '1h', '24h', '7d', '30d'). Default is '24h'."
// @Param        start_time query int false "Start time as a Unix timestamp. Overrides 'range'."
// @Param        end_time query int false "End time as a Unix timestamp. Defaults to now."
// @Success      200  {object}  map[string][]monitor.MonitorLogEntry
// @Failure      404  {object}  map[string]string
// @Router       /monitors/slug/{slug}/checks [get]
func (h *APIHandler) getSlugChecks(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	monitors, err := h.monitorService.GetMonitorsBySlug(slug)
	if err != nil {
		respondWithError(w, http.StatusNotFound, err.Error())
		return
	}
	startTime, endTime, err := parseTimeRange(r)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, err.Error())
		return
	}
	result := make(map[string][]monitor.MonitorLogEntry)
	for _, m := range monitors {
		checks, err := h.monitorService.GetMonitorChecks(m.Slug, m.Name, startTime, endTime)
		if err == nil {
			result[m.Name] = checks
		}
	}
	respondWithJSON(w, http.StatusOK, result)
}

// getMonitorChecks returns detailed time-series data for a monitor within a specified range (by slug and name).
// @Summary      Get monitor checks
// @Description  get detailed time-series data for a monitor within a specified time range
// @Tags         monitors
// @Accept       json
// @Produce      json
// @Param        slug path string true "Slug"
// @Param        name path string true "Monitor Name"
// @Param        range query string false "Time range preset (e.g., '1h', '24h', '7d', '30d'). Default is '24h'."
// @Param        start_time query int false "Start time as a Unix timestamp. Overrides 'range'."
// @Param        end_time query int false "End time as a Unix timestamp. Defaults to now."
// @Success      200  {array}   monitor.MonitorLogEntry
// @Failure      400  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Router       /monitors/{slug}/{name}/checks [get]
// @Router       /monitors/{slug}/{name}/checks [get]
func (h *APIHandler) getMonitorChecks(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	name := chi.URLParam(r, "name")

	// Parse time range parameters from the request URL.
	startTime, endTime, err := parseTimeRange(r)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, err.Error())
		return
	}

	checks, err := h.monitorService.GetMonitorChecks(slug, name, startTime, endTime)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Monitor not found or no data available for the given range")
		return
	}

	respondWithJSON(w, http.StatusOK, checks)
}

// parseTimeRange determines the start and end timestamps from URL query parameters.
// It supports presets like "1h", "24h", "7d", "30d", "90d" and custom "start_time" and "end_time".
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
			} else if rangeStr == "90d" {
				duration = 90 * 24 * time.Hour
			}
		} else {
			return 0, 0, fmt.Errorf("invalid range parameter, use formats like '1h', '24h', '7d', '30d', '90d'")
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
