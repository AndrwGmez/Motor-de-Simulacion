package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/flowverse/flowverse-api/internal/auth"
	"github.com/flowverse/flowverse-api/internal/httpapi"
	"github.com/flowverse/flowverse-api/internal/parser"
	"github.com/flowverse/flowverse-api/internal/runtime"
	"github.com/flowverse/flowverse-api/internal/store"
)

func main() {
	if strings.EqualFold(env("APP_ENV", "development"), "production") {
		gin.SetMode(gin.ReleaseMode)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	repository, closeStore := buildStore(ctx)
	defer closeStore()

	authService := auth.New(repository, auth.Config{
		AccessTTL:  durationEnv("ACCESS_TOKEN_TTL", 15*time.Minute),
		RefreshTTL: durationEnv("REFRESH_TOKEN_TTL", 30*24*time.Hour),
	})
	flowParser := buildParser()
	runManager := runtime.NewManager(repository)
	interrupted, err := runManager.RecoverInterrupted(ctx, time.Now().UTC())
	if err != nil {
		log.Fatalf("recover active runs: %v", err)
	}
	if interrupted > 0 {
		log.Printf("marked %d active runs as interrupted after startup", interrupted)
	}
	server := httpapi.New(repository, authService, flowParser, runManager, httpapi.Config{
		PublicOrigin:  env("PUBLIC_ORIGIN", "http://localhost:3000"),
		SecureCookies: strings.EqualFold(env("APP_ENV", "development"), "production"),
	})
	port := env("PORT", "8080")
	httpServer := &http.Server{
		Addr: ":" + port, Handler: server.Router(),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 75 * time.Second, IdleTimeout: 90 * time.Second,
	}
	go func() {
		log.Printf("flowverse-api listening on :%s with %s store", port, env("STORE_DRIVER", "memory"))
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP server: %v", err)
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown: %v", err)
	}
}

func buildStore(ctx context.Context) (store.Repository, func()) {
	if !strings.EqualFold(env("STORE_DRIVER", "memory"), "postgres") {
		return store.NewMemory(), func() {}
	}
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required when STORE_DRIVER=postgres")
	}
	repository, err := store.OpenPostgres(ctx, databaseURL)
	if err != nil {
		log.Fatalf("open PostgreSQL: %v", err)
	}
	if !strings.EqualFold(env("AUTO_MIGRATE", "true"), "false") {
		if err := repository.Migrate(ctx); err != nil {
			repository.Close()
			log.Fatalf("migrate PostgreSQL: %v", err)
		}
	}
	return repository, repository.Close
}

func buildParser() parser.FlowParser {
	if strings.EqualFold(env("FLOW_PARSER_PROVIDER", "mock"), "openai") {
		return parser.NewOpenAI(os.Getenv("OPENAI_API_KEY"), env("OPENAI_MODEL", "gpt-4.1-mini"), nil)
	}
	return parser.NewMock()
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func durationEnv(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		log.Fatalf("%s must be a Go duration: %v", name, err)
	}
	return duration
}
