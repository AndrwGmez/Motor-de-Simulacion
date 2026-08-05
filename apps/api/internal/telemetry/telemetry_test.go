package telemetry

import (
	"context"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestNormalizeDefaultsAndBounds(t *testing.T) {
	config, err := normalize(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if config.ServiceName != "flowverse-api" || config.SampleRatio != 0 || config.MetricInterval != 30*time.Second {
		t.Fatalf("unexpected defaults: %+v", config)
	}
	if _, err := normalize(Config{SampleRatio: 1.1}); err == nil {
		t.Fatal("expected invalid sample ratio")
	}
	if _, err := normalize(Config{MetricInterval: time.Millisecond}); err == nil {
		t.Fatal("expected invalid metric interval")
	}
}

func TestDisabledSetupAndTraceID(t *testing.T) {
	shutdown, err := Setup(context.Background(), Config{Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	ctx, span := provider.Tracer("test").Start(context.Background(), "operation")
	defer span.End()
	if TraceID(ctx) == "" {
		t.Fatal("expected a valid trace ID")
	}
}
