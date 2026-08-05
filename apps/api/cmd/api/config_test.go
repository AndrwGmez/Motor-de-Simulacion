package main

import (
	"strings"
	"testing"
	"time"
)

func TestLoadStartupConfigFrom(t *testing.T) {
	tests := []struct {
		name    string
		values  map[string]string
		wantErr string
		want    startupConfig
	}{
		{
			name: "development defaults",
			want: startupConfig{
				appEnv: appEnvDevelopment, port: "8080", publicOrigin: "http://localhost:3000", publicWSOrigin: "ws://localhost:8080",
				storeDriver: storeDriverMemory, autoMigrate: true, accessTTL: 15 * time.Minute,
				refreshTTL: 30 * 24 * time.Hour, parserProvider: parserProviderMock, openAIModel: "gpt-4.1-mini",
				copilotProvider: copilotProviderMock, openAICopilotModel: "gpt-4.1-mini",
				otelServiceName: "flowverse-api", otelServiceVersion: "dev", otelSampleRatio: 1,
				otelMetricInterval: 30 * time.Second,
			},
		},
		{
			name: "valid production config is normalized",
			values: map[string]string{
				"APP_ENV": " PRODUCTION ", "STORE_DRIVER": "POSTGRES", "DATABASE_URL": "postgres://db/flowverse?sslmode=verify-full",
				"PUBLIC_ORIGIN": "HTTPS://FLOWVERSE.EXAMPLE/", "PUBLIC_WS_ORIGIN": "WSS://API.FLOWVERSE.EXAMPLE/", "FLOW_PARSER_PROVIDER": "OPENAI",
				"OPENAI_API_KEY": " secret ", "OPENAI_MODEL": "gpt-test", "AUTO_MIGRATE": "false",
				"ACCESS_TOKEN_TTL": "20m", "REFRESH_TOKEN_TTL": "48h", "PORT": "9090",
			},
			want: startupConfig{
				appEnv: appEnvProduction, port: "9090", publicOrigin: "https://flowverse.example", publicWSOrigin: "wss://api.flowverse.example",
				storeDriver: storeDriverPostgres, databaseURL: "postgres://db/flowverse?sslmode=verify-full", autoMigrate: false,
				accessTTL: 20 * time.Minute, refreshTTL: 48 * time.Hour, parserProvider: parserProviderOpenAI,
				openAIAPIKey: "secret", openAIModel: "gpt-test",
				copilotProvider: copilotProviderMock, openAICopilotModel: "gpt-test",
				otelServiceName: "flowverse-api", otelServiceVersion: "dev", otelSampleRatio: 1,
				otelMetricInterval: 30 * time.Second,
			},
		},
		{
			name:   "test environment remains available to CI",
			values: map[string]string{"APP_ENV": "test"},
			want: startupConfig{
				appEnv: appEnvTest, port: "8080", publicOrigin: "http://localhost:3000", publicWSOrigin: "ws://localhost:8080",
				storeDriver: storeDriverMemory, autoMigrate: true, accessTTL: 15 * time.Minute,
				refreshTTL: 30 * 24 * time.Hour, parserProvider: parserProviderMock, openAIModel: "gpt-4.1-mini",
				copilotProvider: copilotProviderMock, openAICopilotModel: "gpt-4.1-mini",
				otelServiceName: "flowverse-api", otelServiceVersion: "dev", otelSampleRatio: 1,
				otelMetricInterval: 30 * time.Second,
			},
		},
		{name: "rejects unknown app environment", values: map[string]string{"APP_ENV": "prodution"}, wantErr: "APP_ENV must be one of"},
		{name: "rejects unknown store driver", values: map[string]string{"STORE_DRIVER": "postgress"}, wantErr: "STORE_DRIVER must be one of"},
		{name: "rejects unknown parser provider", values: map[string]string{"FLOW_PARSER_PROVIDER": "opneai"}, wantErr: "FLOW_PARSER_PROVIDER must be one of"},
		{name: "rejects unknown copilot provider", values: map[string]string{"COPILOT_PROVIDER": "opneai"}, wantErr: "COPILOT_PROVIDER must be one of"},
		{
			name:    "production requires postgres",
			values:  map[string]string{"APP_ENV": "production", "PUBLIC_ORIGIN": "https://flowverse.example"},
			wantErr: "STORE_DRIVER must be \"postgres\"",
		},
		{
			name: "production requires https origin",
			values: map[string]string{
				"APP_ENV": "production", "STORE_DRIVER": "postgres", "DATABASE_URL": "postgres://db/flowverse",
				"PUBLIC_ORIGIN": "http://flowverse.example",
			},
			wantErr: "PUBLIC_ORIGIN must use https",
		},
		{
			name: "production requires secure public websocket origin",
			values: map[string]string{
				"APP_ENV": "production", "STORE_DRIVER": "postgres", "DATABASE_URL": "postgres://db/flowverse",
				"PUBLIC_ORIGIN": "https://flowverse.example", "PUBLIC_WS_ORIGIN": "ws://api.flowverse.example",
			},
			wantErr: "PUBLIC_WS_ORIGIN must use wss",
		},
		{
			name: "production requires explicit migrations",
			values: map[string]string{
				"APP_ENV": "production", "STORE_DRIVER": "postgres", "DATABASE_URL": "postgres://db/flowverse?sslmode=verify-full",
				"PUBLIC_ORIGIN": "https://flowverse.example", "PUBLIC_WS_ORIGIN": "wss://api.flowverse.example",
			},
			wantErr: "AUTO_MIGRATE must be false",
		},
		{
			name: "production requires database TLS",
			values: map[string]string{
				"APP_ENV": "production", "STORE_DRIVER": "postgres", "DATABASE_URL": "postgres://db/flowverse?sslmode=disable",
				"PUBLIC_ORIGIN": "https://flowverse.example", "PUBLIC_WS_ORIGIN": "wss://api.flowverse.example", "AUTO_MIGRATE": "false",
			},
			wantErr: "sslmode=require, verify-ca, or verify-full",
		},
		{name: "postgres requires database URL", values: map[string]string{"STORE_DRIVER": "postgres"}, wantErr: "DATABASE_URL is required"},
		{name: "openai requires API key", values: map[string]string{"FLOW_PARSER_PROVIDER": "openai"}, wantErr: "OPENAI_API_KEY is required"},
		{name: "openai copilot requires API key", values: map[string]string{"COPILOT_PROVIDER": "openai"}, wantErr: "OPENAI_API_KEY is required"},
		{
			name:   "copilot model can be configured independently",
			values: map[string]string{"COPILOT_PROVIDER": "openai", "OPENAI_API_KEY": "key", "OPENAI_MODEL": "parser-model", "OPENAI_COPILOT_MODEL": "copilot-model"},
			want: startupConfig{
				appEnv: appEnvDevelopment, port: "8080", publicOrigin: "http://localhost:3000", publicWSOrigin: "ws://localhost:8080",
				storeDriver: storeDriverMemory, autoMigrate: true, accessTTL: 15 * time.Minute,
				refreshTTL: 30 * 24 * time.Hour, parserProvider: parserProviderMock, openAIAPIKey: "key",
				openAIModel: "parser-model", copilotProvider: copilotProviderOpenAI, openAICopilotModel: "copilot-model",
				otelServiceName: "flowverse-api", otelServiceVersion: "dev", otelSampleRatio: 1,
				otelMetricInterval: 30 * time.Second,
			},
		},
		{name: "rejects origin path", values: map[string]string{"PUBLIC_ORIGIN": "http://localhost:3000/app"}, wantErr: "PUBLIC_ORIGIN"},
		{name: "rejects websocket origin path", values: map[string]string{"PUBLIC_WS_ORIGIN": "ws://localhost:8080/live"}, wantErr: "PUBLIC_WS_ORIGIN"},
		{name: "rejects invalid auto migrate", values: map[string]string{"AUTO_MIGRATE": "truue"}, wantErr: "AUTO_MIGRATE must be a boolean"},
		{name: "rejects invalid access TTL", values: map[string]string{"ACCESS_TOKEN_TTL": "never"}, wantErr: "ACCESS_TOKEN_TTL must be a Go duration"},
		{name: "rejects non-positive refresh TTL", values: map[string]string{"REFRESH_TOKEN_TTL": "0s"}, wantErr: "REFRESH_TOKEN_TTL must be greater than zero"},
		{name: "rejects invalid telemetry toggle", values: map[string]string{"OTEL_ENABLED": "perhaps"}, wantErr: "OTEL_ENABLED must be a boolean"},
		{name: "rejects invalid telemetry ratio", values: map[string]string{"OTEL_SAMPLE_RATIO": "1.1"}, wantErr: "OTEL_SAMPLE_RATIO must be between"},
		{name: "rejects fast telemetry interval", values: map[string]string{"OTEL_METRIC_INTERVAL": "0s"}, wantErr: "OTEL_METRIC_INTERVAL must be greater than zero"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			getenv := func(name string) string { return test.values[name] }
			got, err := loadStartupConfigFrom(getenv)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("loadStartupConfigFrom() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("config = %#v, want %#v", got, test.want)
			}
		})
	}
}
