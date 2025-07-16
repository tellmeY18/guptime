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
	monitorsConfig map[string]MonitorConfig
}

// MonitorConfig holds the configuration for a single monitor from monitors.json.
type MonitorConfig struct {
	URL string `json:"url"`
}

// MonitorLogEntry represents a single log entry for a monitor.
type MonitorLogEntry struct {
	Timestamp int64   `json:"timestamp"`
	Time      float64 `json:"time"`
	Response  string  `json:"response"`
}

// Monitor represents a single configured monitor for API responses.
type Monitor struct {
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
	log.Println("Shutting down monitoring service...")
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
	_, err = db.Exec(`
        CREATE TABLE IF NOT EXISTS monitors (
            name TEXT PRIMARY KEY,
            url TEXT NOT NULL
        );
    `)
	if err != nil {
		return nil, fmt.Errorf("error creating monitors table: %w", err)
	}

	// Create 'log_entries' table if it doesn't exist.
	_, err = db.Exec(`
        CREATE TABLE IF NOT EXISTS log_entries (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            monitor_name TEXT NOT NULL,
            timestamp INTEGER NOT NULL,
            time REAL NOT NULL,
            response TEXT NOT NULL,
            FOREIGN KEY(monitor_name) REFERENCES monitors(name) ON DELETE CASCADE
        );
    `)
	if err != nil {
		return nil, fmt.Errorf("error creating log_entries table: %w", err)
	}

	// Create indexes to improve query performance on the log_entries table.
	_, err = db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_log_entries_monitor_name_timestamp ON log_entries (monitor_name, timestamp);
	`)
	if err != nil {
		return nil, fmt.Errorf("error creating index on log_entries: %w", err)
	}

	log.Println("Database initialized successfully.")
	return db, nil
}

// loadMonitorsConfig reads and parses the monitors.json file.
func (s *Service) loadMonitorsConfig() (map[string]MonitorConfig, error) {
	monitorsFile := filepath.Join(BasePath, "monitors.json")
	monitorsData, err := ioutil.ReadFile(monitorsFile)
	if err != nil {
		return nil, fmt.Errorf("error reading monitors.json: %w", err)
	}

	var rawMonitors map[string]json.RawMessage
	if err := json.Unmarshal(monitorsData, &rawMonitors); err != nil {
		return nil, fmt.Errorf("error parsing monitors.json: %w", err)
	}

	monitorsConfig := make(map[string]MonitorConfig)
	for name, rawData := range rawMonitors {
		var config MonitorConfig
		// First, try to unmarshal as a struct (e.g., {"url": "..."})
		if json.Unmarshal(rawData, &config) == nil && config.URL != "" {
			monitorsConfig[name] = config
			continue
		}

		// If that fails, try to unmarshal as a simple string
		var simpleURL string
		if json.Unmarshal(rawData, &simpleURL) == nil {
			config.URL = simpleURL
			monitorsConfig[name] = config
		} else {
			log.Printf("Warning: Could not parse configuration for monitor '%s'. Skipping.", name)
		}
	}
	return monitorsConfig, nil
}

// addMonitorsToDB syncs the monitors from the config file to the database.
func (s *Service) addMonitorsToDB() error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare("INSERT OR IGNORE INTO monitors (name, url) VALUES (?, ?)")
	if err != nil {
		return fmt.Errorf("failed to prepare insert statement: %w", err)
	}
	defer stmt.Close()

	for name, config := range s.monitorsConfig {
		if _, err := stmt.Exec(name, config.URL); err != nil {
			// Don't stop for one error, but log it.
			log.Printf("Warning: Could not add monitor '%s' to DB: %v", name, err)
		}
	}

	return tx.Commit()
}

// monitorAll iterates through the configured monitors and checks each one.
func (s *Service) monitorAll() {
	log.Println("Running scheduled monitor checks...")
	for name, config := range s.monitorsConfig {
		go s.checkMonitor(name, config.URL)
	}
}

// checkMonitor performs a single HTTP check for a given monitor and saves the result.
func (s *Service) checkMonitor(name, url string) {
	start := time.Now()
	resp, err := http.Get(url)
	elapsed := time.Since(start)
	ms := float64(elapsed.Microseconds()) / 1000.0

	var responseCode string
	if err != nil {
		responseCode = fmt.Sprintf("Error: %v", err)
		log.Printf("Monitor '%s' check failed: %v", name, err)
	} else {
		defer resp.Body.Close()
		responseCode = fmt.Sprintf("%d", resp.StatusCode)
		log.Printf("Monitor '%s' check completed: Status %s, Time %.2fms", name, responseCode, ms)
	}

	logEntry := MonitorLogEntry{
		Timestamp: time.Now().Unix(),
		Time:      ms,
		Response:  responseCode,
	}

	if err := s.saveLogEntry(name, logEntry); err != nil {
		log.Printf("Error saving log entry for monitor '%s': %v", name, err)
	}
}

// saveLogEntry saves a single monitor log entry to the database.
func (s *Service) saveLogEntry(monitorName string, entry MonitorLogEntry) error {
	_, err := s.db.Exec(`
        INSERT INTO log_entries (monitor_name, timestamp, time, response)
        VALUES (?, ?, ?, ?)
    `, monitorName, entry.Timestamp, entry.Time, entry.Response)
	if err != nil {
		return fmt.Errorf("failed to insert log entry for %s: %w", monitorName, err)
	}
	return nil
}

// GetMonitors returns a list of all configured monitors.
func (s *Service) GetMonitors() []Monitor {
	monitors := make([]Monitor, 0, len(s.monitorsConfig))
	for name, config := range s.monitorsConfig {
		monitors = append(monitors, Monitor{Name: name, URL: config.URL})
	}
	// Sort for consistent output.
	sort.Slice(monitors, func(i, j int) bool {
		return monitors[i].Name < monitors[j].Name
	})
	return monitors
}

// GetMonitorSummary calculates and returns the summary for a specific monitor over the last 24 hours.
func (s *Service) GetMonitorSummary(monitorName string) (*MonitorSummary, error) {
	// First, check if the monitor is configured, to avoid querying for something that doesn't exist.
	if _, ok := s.monitorsConfig[monitorName]; !ok {
		return nil, fmt.Errorf("monitor '%s' not found", monitorName)
	}

	var summary MonitorSummary

	// Get the most recent status.
	err := s.db.QueryRow(`
		SELECT response FROM log_entries
		WHERE monitor_name = ?
		ORDER BY timestamp DESC
		LIMIT 1
	`, monitorName).Scan(&summary.CurrentStatus)
	if err != nil {
		if err == sql.ErrNoRows {
			summary.CurrentStatus = "No data yet"
		} else {
			return nil, fmt.Errorf("failed to get current status for %s: %w", monitorName, err)
		}
	}

	// Calculate uptime and average response time over the last 24 hours.
	twentyFourHoursAgo := time.Now().Add(-24 * time.Hour).Unix()
	err = s.db.QueryRow(`
		SELECT
			COALESCE(AVG(CASE WHEN response LIKE '2%' THEN 100.0 ELSE 0.0 END), 0),
			COALESCE(AVG(time), 0)
		FROM log_entries
		WHERE monitor_name = ? AND timestamp >= ?
	`, monitorName, twentyFourHoursAgo).Scan(&summary.UptimePercentage24h, &summary.AverageResponseTime24h)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate summary statistics for %s: %w", monitorName, err)
	}

	return &summary, nil
}

// GetMonitorChecks retrieves detailed check logs for a monitor within a given time range.
func (s *Service) GetMonitorChecks(monitorName string, start, end int64) ([]MonitorLogEntry, error) {
	if _, ok := s.monitorsConfig[monitorName]; !ok {
		return nil, fmt.Errorf("monitor '%s' not found", monitorName)
	}

	rows, err := s.db.Query(`
		SELECT timestamp, time, response
		FROM log_entries
		WHERE monitor_name = ? AND timestamp >= ? AND timestamp <= ?
		ORDER BY timestamp ASC
	`, monitorName, start, end)
	if err != nil {
		return nil, fmt.Errorf("failed to query checks for %s: %w", monitorName, err)
	}
	defer rows.Close()

	var checks []MonitorLogEntry
	for rows.Next() {
		var entry MonitorLogEntry
		if err := rows.Scan(&entry.Timestamp, &entry.Time, &entry.Response); err != nil {
			return nil, fmt.Errorf("failed to scan check entry for %s: %w", monitorName, err)
		}
		checks = append(checks, entry)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error during rows iteration for %s: %w", monitorName, err)
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
	log.Printf("Purging log entries older than %s (timestamp %d)...", time.Unix(cutoff, 0).Format(time.RFC3339), cutoff)

	result, err := s.db.Exec(`DELETE FROM log_entries WHERE timestamp < ?`, cutoff)
	if err != nil {
		log.Printf("Error: Failed to purge old log entries: %v", err)
		return
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		log.Printf("Error: Failed to retrieve number of purged rows: %v", err)
		return
	}

	log.Printf("Successfully purged %d old log entries.", rowsAffected)
}
