package main

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	appEnvDevelopment = "development"
	appEnvTest        = "test"
	appEnvProduction  = "production"

	storeDriverMemory   = "memory"
	storeDriverPostgres = "postgres"

	parserProviderMock   = "mock"
	parserProviderOpenAI = "openai"

	copilotProviderMock   = "mock"
	copilotProviderOpenAI = "openai"
)

type startupConfig struct {
	appEnv             string
	port               string
	publicOrigin       string
	publicWSOrigin     string
	storeDriver        string
	databaseURL        string
	autoMigrate        bool
	accessTTL          time.Duration
	refreshTTL         time.Duration
	parserProvider     string
	openAIAPIKey       string
	openAIModel        string
	copilotProvider    string
	openAICopilotModel string
	otelEnabled        bool
	otelServiceName    string
	otelServiceVersion string
	otelSampleRatio    float64
	otelMetricInterval time.Duration
}

func (config startupConfig) isProduction() bool {
	return config.appEnv == appEnvProduction
}

func loadStartupConfig() (startupConfig, error) {
	return loadStartupConfigFrom(os.Getenv)
}

func loadStartupConfigFrom(getenv func(string) string) (startupConfig, error) {
	appEnv, err := enumValue(getenv, "APP_ENV", appEnvDevelopment, appEnvDevelopment, appEnvTest, appEnvProduction)
	if err != nil {
		return startupConfig{}, err
	}
	storeDriver, err := enumValue(getenv, "STORE_DRIVER", storeDriverMemory, storeDriverMemory, storeDriverPostgres)
	if err != nil {
		return startupConfig{}, err
	}
	parserProvider, err := enumValue(getenv, "FLOW_PARSER_PROVIDER", parserProviderMock, parserProviderMock, parserProviderOpenAI)
	if err != nil {
		return startupConfig{}, err
	}
	copilotProvider, err := enumValue(getenv, "COPILOT_PROVIDER", copilotProviderMock, copilotProviderMock, copilotProviderOpenAI)
	if err != nil {
		return startupConfig{}, err
	}

	publicOrigin, originScheme, err := canonicalOrigin(envValue(getenv, "PUBLIC_ORIGIN", "http://localhost:3000"))
	if err != nil {
		return startupConfig{}, fmt.Errorf("PUBLIC_ORIGIN: %w", err)
	}
	if appEnv == appEnvProduction && storeDriver != storeDriverPostgres {
		return startupConfig{}, fmt.Errorf("STORE_DRIVER must be %q when APP_ENV=%s", storeDriverPostgres, appEnvProduction)
	}
	if appEnv == appEnvProduction && originScheme != "https" {
		return startupConfig{}, fmt.Errorf("PUBLIC_ORIGIN must use https when APP_ENV=%s", appEnvProduction)
	}
	publicWSOrigin, websocketScheme, err := canonicalWebSocketOrigin(envValue(getenv, "PUBLIC_WS_ORIGIN", "ws://localhost:8080"))
	if err != nil {
		return startupConfig{}, fmt.Errorf("PUBLIC_WS_ORIGIN: %w", err)
	}
	if appEnv == appEnvProduction && websocketScheme != "wss" {
		return startupConfig{}, fmt.Errorf("PUBLIC_WS_ORIGIN must use wss when APP_ENV=%s", appEnvProduction)
	}

	databaseURL := strings.TrimSpace(getenv("DATABASE_URL"))
	if storeDriver == storeDriverPostgres && databaseURL == "" {
		return startupConfig{}, fmt.Errorf("DATABASE_URL is required when STORE_DRIVER=%s", storeDriverPostgres)
	}
	openAIAPIKey := strings.TrimSpace(getenv("OPENAI_API_KEY"))
	if parserProvider == parserProviderOpenAI && openAIAPIKey == "" {
		return startupConfig{}, fmt.Errorf("OPENAI_API_KEY is required when FLOW_PARSER_PROVIDER=%s", parserProviderOpenAI)
	}
	if copilotProvider == copilotProviderOpenAI && openAIAPIKey == "" {
		return startupConfig{}, fmt.Errorf("OPENAI_API_KEY is required when COPILOT_PROVIDER=%s", copilotProviderOpenAI)
	}

	autoMigrate, err := boolValue(getenv, "AUTO_MIGRATE", true)
	if err != nil {
		return startupConfig{}, err
	}
	if appEnv == appEnvProduction && autoMigrate {
		return startupConfig{}, fmt.Errorf("AUTO_MIGRATE must be false when APP_ENV=%s; run migrations as an explicit deployment step", appEnvProduction)
	}
	if appEnv == appEnvProduction {
		if err := validateProductionDatabaseURL(databaseURL); err != nil {
			return startupConfig{}, fmt.Errorf("DATABASE_URL: %w", err)
		}
	}
	accessTTL, err := durationValue(getenv, "ACCESS_TOKEN_TTL", 15*time.Minute)
	if err != nil {
		return startupConfig{}, err
	}
	refreshTTL, err := durationValue(getenv, "REFRESH_TOKEN_TTL", 30*24*time.Hour)
	if err != nil {
		return startupConfig{}, err
	}
	otelEnabled, err := boolValue(getenv, "OTEL_ENABLED", false)
	if err != nil {
		return startupConfig{}, err
	}
	otelSampleRatio, err := boundedFloatValue(getenv, "OTEL_SAMPLE_RATIO", 1, 0, 1)
	if err != nil {
		return startupConfig{}, err
	}
	otelMetricInterval, err := durationValue(getenv, "OTEL_METRIC_INTERVAL", 30*time.Second)
	if err != nil {
		return startupConfig{}, err
	}

	openAIModel := envValue(getenv, "OPENAI_MODEL", "gpt-4.1-mini")
	return startupConfig{
		appEnv:             appEnv,
		port:               envValue(getenv, "PORT", "8080"),
		publicOrigin:       publicOrigin,
		publicWSOrigin:     publicWSOrigin,
		storeDriver:        storeDriver,
		databaseURL:        databaseURL,
		autoMigrate:        autoMigrate,
		accessTTL:          accessTTL,
		refreshTTL:         refreshTTL,
		parserProvider:     parserProvider,
		openAIAPIKey:       openAIAPIKey,
		openAIModel:        openAIModel,
		copilotProvider:    copilotProvider,
		openAICopilotModel: envValue(getenv, "OPENAI_COPILOT_MODEL", openAIModel),
		otelEnabled:        otelEnabled,
		otelServiceName:    envValue(getenv, "OTEL_SERVICE_NAME", "flowverse-api"),
		otelServiceVersion: envValue(getenv, "OTEL_SERVICE_VERSION", "dev"),
		otelSampleRatio:    otelSampleRatio,
		otelMetricInterval: otelMetricInterval,
	}, nil
}

func envValue(getenv func(string) string, name, fallback string) string {
	if value := strings.TrimSpace(getenv(name)); value != "" {
		return value
	}
	return fallback
}

func enumValue(getenv func(string) string, name, fallback string, allowed ...string) (string, error) {
	value := strings.ToLower(envValue(getenv, name, fallback))
	for _, candidate := range allowed {
		if value == candidate {
			return value, nil
		}
	}
	return "", fmt.Errorf("%s must be one of %s; got %q", name, strings.Join(allowed, ", "), value)
}

func boolValue(getenv func(string) string, name string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", name, err)
	}
	return value, nil
}

func durationValue(getenv func(string) string, name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a Go duration: %w", name, err)
	}
	if value <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", name)
	}
	return value, nil
}

func boundedFloatValue(
	getenv func(string) string,
	name string,
	fallback, minimum, maximum float64,
) (float64, error) {
	raw := strings.TrimSpace(getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number: %w", name, err)
	}
	if value < minimum || value > maximum {
		return 0, fmt.Errorf("%s must be between %g and %g", name, minimum, maximum)
	}
	return value, nil
}

func canonicalOrigin(raw string) (string, string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("must be an absolute http(s) origin: %w", err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if (scheme != "http" && scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return "", "", fmt.Errorf("must be an absolute http(s) origin")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.EscapedPath() != "" && parsed.EscapedPath() != "/") {
		return "", "", fmt.Errorf("must not contain a path, query, fragment, or credentials")
	}
	return scheme + "://" + strings.ToLower(parsed.Host), scheme, nil
}

func canonicalWebSocketOrigin(raw string) (string, string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("must be an absolute ws(s) origin: %w", err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if (scheme != "ws" && scheme != "wss") || parsed.Host == "" || parsed.User != nil {
		return "", "", fmt.Errorf("must be an absolute ws(s) origin")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.EscapedPath() != "" && parsed.EscapedPath() != "/") {
		return "", "", fmt.Errorf("must not contain a path, query, fragment, or credentials")
	}
	return scheme + "://" + strings.ToLower(parsed.Host), scheme, nil
}

func validateProductionDatabaseURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Host == "" {
		return fmt.Errorf("must be an absolute PostgreSQL URL")
	}
	switch strings.ToLower(parsed.Query().Get("sslmode")) {
	case "require", "verify-ca", "verify-full":
		return nil
	default:
		return fmt.Errorf("must set sslmode=require, verify-ca, or verify-full in production")
	}
}
