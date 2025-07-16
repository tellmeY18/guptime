package monitor

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"path/filepath"
	"sort"
	"time"

	_ "github.com/mattn/go-sqlite3" // SQLite driver
)

const (
	// BasePath is the root directory for monitor-related files like the database and config.
	BasePath = "."
	// DefaultCheckInterval is the time between monitoring checks.
	DefaultCheckInterval = 5 * time.Minute
)

// Config holds the configuration for the monitoring service.
type Config struct {
	DBPath        string
	CheckInterval time.Duration
	RetentionDays int
}

// Service encapsulates the monitoring logic and its dependencies.
type Service struct {
	db             *sql.DB
	checkInterval  time.Duration
	retentionPeriod time.Duration
	monitorsConfig []Monitor
}

// MonitorConfig (old struct, no longer used for monitors.json parsing directly)
// type MonitorConfig struct {
// 	URL string `json:"url"`
// }

// MonitorLogEntry represents a single log entry for a monitor.
type MonitorLogEntry struct {
	Timestamp int64   `json:"timestamp"`
	Time      float64 `json:"time"`
	Response  string  `json:"response"`
}

// Monitor represents a single configured monitor for API responses, including the new slug field.
type Monitor struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

// MonitorSummary provides high-level aggregated data for a monitor.
type MonitorSummary struct {
	CurrentStatus          string  `json:"current_status"`
	UptimePercentage24h    float64 `json:"uptime_percentage_24h"`
	AverageResponseTime24h float64 `json:"average_response_time_24h"`
}

// NewService creates and initializes a new monitoring service.
func NewService(config *Config) (*Service, error) {
	db, err := initializeDatabase(config.DBPath)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	return &Service{
		db:              db,
		checkInterval:   config.CheckInterval,
		retentionPeriod: time.Duration(config.RetentionDays) * 24 * time.Hour,
	}, nil
}

// Start begins the monitoring process. It loads monitor configurations and runs checks periodically.
func (s *Service) Start() error {
	log.Println("Starting monitoring service...")

	config, err := s.loadMonitorsConfig()
	if err != nil {
		return fmt.Errorf("could not load monitors configuration: %w", err)
	}
	s.monitorsConfig = config

	if len(s.monitorsConfig) == 0 {
		log.Println("Warning: No monitors found in monitors.json. Monitoring will not start.")
		return nil
	}

	if err := s.addMonitorsToDB(); err != nil {
		return fmt.Errorf("error ensuring monitors are in the database: %w", err)
	}

	// Start the monitoring process in a background goroutine.
	go func() {
		// Perform an initial check immediately.
		s.monitorAll()

		// Then, continue checking on a ticker.
		ticker := time.NewTicker(s.checkInterval)
		defer ticker.Stop()

		for range ticker.C {
			s.monitorAll()
		}
	}()

	// Start the data retention cron job in the background.
	go s.startRetentionCron()

	return nil
}

// Close gracefully shuts down the service by closing the database connection.
func (s *Service) Close() {
	log.Println("Shutting down monitoring service...\n")
	if s.db != nil {
		s.db.Close()
	}
}

// initializeDatabase connects to the SQLite database and ensures the necessary tables are created.
func initializeDatabase(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_foreign_keys=true")
	if err != nil {
		return nil, fmt.Errorf("error opening database: %w", err)
	}

	// Create 'monitors' table if it doesn't exist.
	// Added 'slug' field and a composite primary key (slug, name) for uniqueness.
	_, err = db.Exec(`
        CREATE TABLE IF NOT EXISTS monitors (
            slug TEXT NOT NULL,
            name TEXT NOT NULL,
            url TEXT NOT NULL,
            PRIMARY KEY (slug, name)
        );
    `)
	if err != nil {
		return nil, fmt.Errorf("error creating monitors table: %w", err)
	}

	// Create 'log_entries' table if it doesn't exist.
	// Updated FOREIGN KEY to reference both slug and name from the monitors table.
	_, err = db.Exec(`
        CREATE TABLE IF NOT EXISTS log_entries (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            monitor_slug TEXT NOT NULL,
            monitor_name TEXT NOT NULL,
            timestamp INTEGER NOT NULL,
            time REAL NOT NULL,
            response TEXT NOT NULL,
            FOREIGN KEY(monitor_slug, monitor_name) REFERENCES monitors(slug, name) ON DELETE CASCADE
        );
    `)
	if err != nil {
		return nil, fmt.Errorf("error creating log_entries table: %w", err)
	}

	// Create indexes to improve query performance on the log_entries table.
	// Updated index to include monitor_slug as well.
	_, err = db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_log_entries_monitor_slug_name_timestamp ON log_entries (monitor_slug, monitor_name, timestamp);
	`)
	if err != nil {
		return nil, fmt.Errorf("error creating index on log_entries: %w", err)
	}

	log.Println("Database initialized successfully.")
	return db, nil
}

// loadMonitorsConfig reads and parses the monitors.json file, now expecting an array of Monitor objects.
func (s *Service) loadMonitorsConfig() ([]Monitor, error) {
	monitorsFile := filepath.Join(BasePath, "monitors.json")
	monitorsData, err := ioutil.ReadFile(monitorsFile)
	if err != nil {
		return nil, fmt.Errorf("error reading monitors.json: %w", err)
	}

	var monitors []Monitor
	if err := json.Unmarshal(monitorsData, &monitors); err != nil {
		return nil, fmt.Errorf("error parsing monitors.json: %w", err)
	}

	return monitors, nil
}

// addMonitorsToDB syncs the monitors from the config file to the database.
// Now inserts slug, name, and url.
func (s *Service) addMonitorsToDB() error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Clear existing monitors to prevent orphaned log entries and simplify updates
	// when monitor definitions change or are removed.
	if _, err := tx.Exec("DELETE FROM monitors"); err != nil {
		return fmt.Errorf("failed to clear existing monitors: %w", err)
	}


	stmt, err := tx.Prepare("INSERT INTO monitors (slug, name, url) VALUES (?, ?, ?)")
	if err != nil {
		return fmt.Errorf("failed to prepare insert statement: %w", err)
	}
	defer stmt.Close()

	for _, monitor := range s.monitorsConfig {
		// Use INSERT OR REPLACE if you want to update existing monitors on slug/name conflict
		// For now, simple INSERT IGNORE assumes monitor definitions are largely static or handled by DELETE above
		if _, err := stmt.Exec(monitor.Slug, monitor.Name, monitor.URL); err != nil {
			// Don't stop for one error, but log it.
			log.Printf("Warning: Could not add monitor '%s/%s' to DB: %v", monitor.Slug, monitor.Name, err)
		}
	}

	return tx.Commit()
}

// monitorAll iterates through the configured monitors and checks each one.
func (s *Service) monitorAll() {
	log.Println("Running scheduled monitor checks...\n")
	for _, monitor := range s.monitorsConfig {
		go s.checkMonitor(monitor.Slug, monitor.Name, monitor.URL)
	}
}

// checkMonitor performs a single HTTP check for a given monitor and saves the result.
// Now accepts slug and name.
func (s *Service) checkMonitor(slug, name, url string) {
	start := time.Now()
	resp, err := http.Get(url)
	elapsed := time.Since(start)
	ms := float64(elapsed.Microseconds()) / 1000.0

	var responseCode string
	if err != nil {
		responseCode = fmt.Sprintf("Error: %v", err)
		log.Printf("Monitor '%s/%s' check failed: %v\n", slug, name, err)
	} else {
		defer resp.Body.Close()
		responseCode = fmt.Sprintf("%d", resp.StatusCode)
		log.Printf("Monitor '%s/%s' check completed: Status %s, Time %.2fms\n", slug, name, responseCode, ms)
	}

	logEntry := MonitorLogEntry{
		Timestamp: time.Now().Unix(),
		Time:      ms,
		Response:  responseCode,
	}

	if err := s.saveLogEntry(slug, name, logEntry); err != nil {
		log.Printf("Error saving log entry for monitor '%s/%s': %v\n", slug, name, err)
	}
}

// saveLogEntry saves a single monitor log entry to the database.
// Now accepts monitorSlug and monitorName.
func (s *Service) saveLogEntry(monitorSlug, monitorName string, entry MonitorLogEntry) error {
	_, err := s.db.Exec(`
        INSERT INTO log_entries (monitor_slug, monitor_name, timestamp, time, response)
        VALUES (?, ?, ?, ?, ?)
    `, monitorSlug, monitorName, entry.Timestamp, entry.Time, entry.Response)
	if err != nil {
		return fmt.Errorf("failed to insert log entry for %s/%s: %w", monitorSlug, monitorName, err)
	}
	return nil
}

// GetMonitors returns a list of all configured monitors.
func (s *Service) GetMonitors() []Monitor {
	// Directly return the loaded monitorsConfig as it now matches the Monitor struct
	monitors := make([]Monitor, len(s.monitorsConfig))
	copy(monitors, s.monitorsConfig)
	// Sort for consistent output.
	sort.Slice(monitors, func(i, j int) bool {
		if monitors[i].Slug != monitors[j].Slug {
			return monitors[i].Slug < monitors[j].Slug
		}
		return monitors[i].Name < monitors[j].Name
	})
	return monitors
}

// GetMonitorsBySlug returns a list of monitors associated with a specific slug.
func (s *Service) GetMonitorsBySlug(slug string) ([]Monitor, error) {
	var monitors []Monitor
	rows, err := s.db.Query(`SELECT slug, name, url FROM monitors WHERE slug = ?`, slug)
	if err != nil {
		return nil, fmt.Errorf("failed to query monitors for slug '%s': %w", slug, err)
	}
	defer rows.Close()

	for rows.Next() {
		var m Monitor
		if err := rows.Scan(&m.Slug, &m.Name, &m.URL); err != nil {
			return nil, fmt.Errorf("failed to scan monitor for slug '%s': %w", slug, err)
		}
		monitors = append(monitors, m)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error during rows iteration for slug '%s': %w", slug, err)
	}

	if len(monitors) == 0 {
		return nil, fmt.Errorf("no monitors found for slug '%s'", slug)
	}

	return monitors, nil
}


// GetMonitorSummary calculates and returns the summary for a specific monitor over the last 24 hours.
// Now accepts slug and name.
func (s *Service) GetMonitorSummary(monitorSlug, monitorName string) (*MonitorSummary, error) {
	// First, check if the monitor is configured, to avoid querying for something that doesn't exist.
	// This check relies on the in-memory config which might not be exhaustive if DB was manually altered.
	// A more robust check might query the DB for existence of (slug, name) pair.
	found := false
	for _, m := range s.monitorsConfig {
		if m.Slug == monitorSlug && m.Name == monitorName {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("monitor '%s/%s' not found in configuration", monitorSlug, monitorName)
	}

	var summary MonitorSummary

	// Get the most recent status.
	err := s.db.QueryRow(`
		SELECT response FROM log_entries
		WHERE monitor_slug = ? AND monitor_name = ?
		ORDER BY timestamp DESC
		LIMIT 1
	`, monitorSlug, monitorName).Scan(&summary.CurrentStatus)
	if err != nil {
		if err == sql.ErrNoRows {
			summary.CurrentStatus = "No data yet"
		} else {
			return nil, fmt.Errorf("failed to get current status for %s/%s: %w", monitorSlug, monitorName, err)
		}
	}

	// Calculate uptime and average response time over the last 24 hours.
	twentyFourHoursAgo := time.Now().Add(-24 * time.Hour).Unix()
	err = s.db.QueryRow(`
		SELECT
			COALESCE(AVG(CASE WHEN response LIKE '2%' THEN 100.0 ELSE 0.0 END), 0),
			COALESCE(AVG(time), 0)
		FROM log_entries
		WHERE monitor_slug = ? AND monitor_name = ? AND timestamp >= ?
	`, monitorSlug, monitorName, twentyFourHoursAgo).Scan(&summary.UptimePercentage24h, &summary.AverageResponseTime24h)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate summary statistics for %s/%s: %w", monitorSlug, monitorName, err)
	}

	return &summary, nil
}

// GetMonitorChecks retrieves detailed check logs for a monitor within a given time range.
// Now accepts slug and name.
func (s *Service) GetMonitorChecks(monitorSlug, monitorName string, start, end int64) ([]MonitorLogEntry, error) {
	found := false
	for _, m := range s.monitorsConfig {
		if m.Slug == monitorSlug && m.Name == monitorName {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("monitor '%s/%s' not found in configuration", monitorSlug, monitorName)
	}

	rows, err := s.db.Query(`
		SELECT timestamp, time, response
		FROM log_entries
		WHERE monitor_slug = ? AND monitor_name = ? AND timestamp >= ? AND timestamp <= ?
		ORDER BY timestamp ASC
	`, monitorSlug, monitorName, start, end)
	if err != nil {
		return nil, fmt.Errorf("failed to query checks for %s/%s: %w", monitorSlug, monitorName, err)
	}
	defer rows.Close()

	var checks []MonitorLogEntry
	for rows.Next() {
		var entry MonitorLogEntry
		if err := rows.Scan(&entry.Timestamp, &entry.Time, &entry.Response); err != nil {
			return nil, fmt.Errorf("failed to scan check entry for %s/%s: %w", monitorSlug, monitorName, err)
		}
		checks = append(checks, entry)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error during rows iteration for %s/%s: %w", monitorSlug, monitorName, err)
	}

	return checks, nil
}

// startRetentionCron runs a periodic job to purge old records from the database.
func (s *Service) startRetentionCron() {
	// For a long-running service, a more robust cron library might be better,
	// but a ticker is simple and effective for a daily task.
	log.Println("Starting data retention cron job...")
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	// Run once on startup, then continue on the ticker schedule.
	s.purgeOldRecords()

	for range ticker.C {
		s.purgeOldRecords()
	}
}

// purgeOldRecords deletes log entries from the database that are older than the configured retention period.
func (s *Service) purgeOldRecords() {
	// Calculate the cutoff timestamp. Any record older than this will be deleted.
	cutoff := time.Now().Add(-s.retentionPeriod).Unix()
	log.Printf("Purging log entries older than %s (timestamp %d)...\n", time.Unix(cutoff, 0).Format(time.RFC3339), cutoff)

	result, err := s.db.Exec(`DELETE FROM log_entries WHERE timestamp < ?`, cutoff)
	if err != nil {
		log.Printf("Error: Failed to purge old log entries: %v\n", err)
		return
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		log.Printf("Error: Failed to retrieve number of purged rows: %v\n", err)
		return
	}

	log.Printf("Successfully purged %d old log entries.\n", rowsAffected)
}
