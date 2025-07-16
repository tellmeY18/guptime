package main

import (
	"log"
	"os"
	"strconv"
	"time"
)

// Config holds the application's configuration values.
type Config struct {
	DBPath        string
	ServerPort    string
	CheckInterval time.Duration
	RetentionDays int
	Environment   string
	CORSAllowedHosts []string
}

// LoadConfig loads configuration from environment variables, providing sensible defaults.
func LoadConfig() (*Config, error) {
	// Get the application environment, default to 'development'.
	env := getEnv("ENVIRONMENT", "development")

	// Get the database path, default to './data.db'.
	dbPath := getEnv("DB_PATH", "./data.db")

	// Get the server port, default to '8080'.
	serverPort := getEnv("HTTP_PORT", "8080")

	// Get the monitoring check interval, default to '5m'.
	checkIntervalStr := getEnv("CHECK_INTERVAL", "5m")
	checkInterval, err := time.ParseDuration(checkIntervalStr)
	if err != nil {
		return nil, err
	}

	// Get the data retention period in days, default to '90'.
	retentionDaysStr := getEnv("RETENTION_DAYS", "90")
	retentionDays, err := strconv.Atoi(retentionDaysStr)
	if err != nil {
		return nil, err
	}

	// Get CORS allowed hosts, comma-separated, default to "*"
	corsAllowedHostsStr := getEnv("CORS_ALLOWED_HOSTS", "*")
	var corsAllowedHosts []string
	for _, h := range splitAndTrim(corsAllowedHostsStr, ",") {
		if h != "" {
			corsAllowedHosts = append(corsAllowedHosts, h)
		}
	}

	conf := &Config{
		Environment:      env,
		DBPath:           dbPath,
		ServerPort:       ":" + serverPort,
		CheckInterval:    checkInterval,
		RetentionDays:    retentionDays,
		CORSAllowedHosts: corsAllowedHosts,
	}

	log.Printf("Configuration loaded: %+v", conf)
	return conf, nil
}

// splitAndTrim splits a string by sep and trims whitespace from each element.
func splitAndTrim(s, sep string) []string {
	var result []string
	for _, part := range split(s, sep) {
		trimmed := trim(part)
		result = append(result, trimmed)
	}
	return result
}

// split splits a string by sep.
func split(s, sep string) []string {
	var result []string
	start := 0
	for i := 0; i+len(sep) <= len(s); {
		if s[i:i+len(sep)] == sep {
			result = append(result, s[start:i])
			i += len(sep)
			start = i
		} else {
			i++
		}
	}
	result = append(result, s[start:])
	return result
}

// trim trims leading and trailing whitespace from a string.
func trim(s string) string {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

// getEnv retrieves an environment variable or returns a fallback value.
func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
