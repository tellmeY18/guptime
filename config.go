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

	conf := &Config{
		Environment:   env,
		DBPath:        dbPath,
		ServerPort:    ":" + serverPort,
		CheckInterval: checkInterval,
		RetentionDays: retentionDays,
	}

	log.Printf("Configuration loaded: %+v", conf)
	return conf, nil
}

// getEnv retrieves an environment variable or returns a fallback value.
func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
