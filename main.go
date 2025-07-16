package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"
	httpSwagger "github.com/swaggo/http-swagger"

	"guptime/api"
	_ "guptime/docs" // This blank import is required for swag to find your docs.
	"guptime/monitor"
)

// @title           Guptime API
// @version         1.0
// @description     An API for monitoring website uptime and performance.
// @termsOfService  http://swagger.io/terms/
// @contact.name   API Support
// @contact.url    http://www.swagger.io/support
// @contact.email  support@swagger.io
// @license.name  MIT
// @license.url   https://opensource.org/licenses/MIT
// @host      localhost:8080
// @BasePath  /api/v1
func main() {
	// Load .env file. It's okay if it does not exist.
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables and defaults")
	}

	// --- Setup Logging ---
	log.SetOutput(os.Stdout)
	log.Println("Initializing Guptime API service...")

	// --- Load Configuration ---
	config, err := LoadConfig()
	if err != nil {
		log.Fatalf("Fatal: Failed to load configuration: %v", err)
	}

	// --- Initialize Monitoring Service ---
	// The monitor service runs in the background, handling all monitoring tasks.
	monitorConfig := &monitor.Config{
		DBPath:        config.DBPath,
		CheckInterval: config.CheckInterval,
		RetentionDays: config.RetentionDays,
	}
	monitorService, err := monitor.NewService(monitorConfig)
	if err != nil {
		log.Fatalf("Fatal: Failed to initialize monitor service: %v", err)
	}

	// Start the monitoring service. It will run its own goroutines for checks.
	if err := monitorService.Start(); err != nil {
		log.Fatalf("Fatal: Failed to start monitor service: %v", err)
	}

	// --- Initialize API Handler ---
	apiHandler := api.NewAPIHandler(monitorService)

	// --- Setup Chi Router for the API ---
	r := chi.NewRouter()

	// --- CORS Middleware ---
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			allowed := false
			origin := req.Header.Get("Origin")
			for _, h := range config.CORSAllowedHosts {
				if h == "*" || h == origin {
					allowed = true
					break
				}
			}
			if allowed && origin != "" {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}
			if req.Method == "OPTIONS" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, req)
		})
	})

	// A good base middleware stack.
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	// --- API Routes ---
	log.Println("Registering API routes...")
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status": "ok", "service": "guptime-api"}`))
	})

	// Swagger documentation endpoint, only enabled in development.
	if config.Environment == "development" {
		log.Println("Registering Swagger documentation endpoint at /swagger/*")
		r.Get("/swagger/*", httpSwagger.Handler(
			httpSwagger.URL("/swagger/doc.json"), // The url pointing to API definition
		))
	}

	// API version 1 routes.
	r.Route("/api/v1", func(r chi.Router) {
		apiHandler.RegisterRoutes(r)
	})

	// --- Start Server & Handle Graceful Shutdown ---
	server := &http.Server{Addr: config.ServerPort, Handler: r}
	serverCtx, serverStopCtx := context.WithCancel(context.Background())

	// Listen for OS signals to trigger a graceful shutdown.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	go func() {
		<-sig
		log.Println("Shutdown signal received.")

		shutdownCtx, cancel := context.WithTimeout(serverCtx, 30*time.Second)
		defer cancel()

		go func() {
			<-shutdownCtx.Done()
			if shutdownCtx.Err() == context.DeadlineExceeded {
				log.Fatal("Graceful shutdown timed out... forcing exit.")
			}
		}()

		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Fatalf("Server shutdown failed: %v", err)
		}

		monitorService.Close()
		serverStopCtx()
	}()

	// Run the server.
	log.Printf("API Server is listening on port %s", config.ServerPort)
	if config.Environment == "development" {
		log.Printf("Swagger UI is available at http://localhost%s/swagger/index.html", config.ServerPort)
	}
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Server failed to listen and serve: %v", err)
	}

	<-serverCtx.Done()
	log.Println("Shutdown complete.")
}
