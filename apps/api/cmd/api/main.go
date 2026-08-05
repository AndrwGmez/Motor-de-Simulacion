package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/flowverse/flowverse-api/internal/auth"
	"github.com/flowverse/flowverse-api/internal/copilot"
	"github.com/flowverse/flowverse-api/internal/httpapi"
	"github.com/flowverse/flowverse-api/internal/parser"
	"github.com/flowverse/flowverse-api/internal/runtime"
	"github.com/flowverse/flowverse-api/internal/store"
	"github.com/flowverse/flowverse-api/internal/telemetry"
)

func main() {
	config, err := loadStartupConfig()
	if err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}
	if config.isProduction() {
		gin.SetMode(gin.ReleaseMode)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	shutdownTelemetry, err := telemetry.Setup(ctx, telemetry.Config{
		Enabled: config.otelEnabled, ServiceName: config.otelServiceName,
		ServiceVersion: config.otelServiceVersion, Environment: config.appEnv,
		SampleRatio: config.otelSampleRatio, MetricInterval: config.otelMetricInterval,
	})
	if err != nil {
		log.Fatalf("initialize OpenTelemetry: %v", err)
	}
	defer func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTelemetry(shutdownContext); err != nil {
			log.Printf("shutdown OpenTelemetry: %v", err)
		}
	}()

	repository, closeStore, err := buildStore(ctx, config)
	if err != nil {
		log.Fatalf("initialize store: %v", err)
	}
	defer closeStore()

	authService := auth.New(repository, auth.Config{
		AccessTTL:  config.accessTTL,
		RefreshTTL: config.refreshTTL,
	})
	flowParser := buildParser(config)
	runManager := runtime.NewManager(repository)
	interrupted, err := runManager.RecoverInterrupted(ctx, time.Now().UTC())
	if err != nil {
		log.Fatalf("recover active runs: %v", err)
	}
	if interrupted > 0 {
		log.Printf("marked %d active runs as interrupted after startup", interrupted)
	}
	server := httpapi.New(repository, authService, flowParser, runManager, httpapi.Config{
		PublicOrigin:          config.publicOrigin,
		PublicWebSocketOrigin: config.publicWSOrigin,
		SecureCookies:         config.isProduction(),
		CopilotProvider:       buildCopilot(config),
	})
	httpServer := &http.Server{
		Addr: ":" + config.port, Handler: server.Router(),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 75 * time.Second, IdleTimeout: 90 * time.Second,
	}
	go func() {
		log.Printf("flowverse-api listening on :%s with %s store", config.port, config.storeDriver)
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

func buildStore(ctx context.Context, config startupConfig) (store.Repository, func(), error) {
	if config.storeDriver == storeDriverMemory {
		return store.NewMemory(), func() {}, nil
	}
	repository, err := store.OpenPostgres(ctx, config.databaseURL)
	if err != nil {
		return nil, nil, err
	}
	if config.autoMigrate {
		if err := repository.Migrate(ctx); err != nil {
			repository.Close()
			return nil, nil, err
		}
	}
	return repository, repository.Close, nil
}

func buildParser(config startupConfig) parser.FlowParser {
	if config.parserProvider == parserProviderOpenAI {
		return parser.NewOpenAI(config.openAIAPIKey, config.openAIModel, nil)
	}
	return parser.NewMock()
}

func buildCopilot(config startupConfig) copilot.Provider {
	if config.copilotProvider == copilotProviderOpenAI {
		return copilot.NewOpenAI(config.openAIAPIKey, config.openAICopilotModel, nil)
	}
	return copilot.NewMock()
}
