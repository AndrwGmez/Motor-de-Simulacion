package telemetry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.32.0"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationScope = "github.com/flowverse/flowverse-api"

type Config struct {
	Enabled        bool
	ServiceName    string
	ServiceVersion string
	Environment    string
	SampleRatio    float64
	MetricInterval time.Duration
}

type Shutdown func(context.Context) error

type instruments struct {
	runsStarted        metric.Int64Counter
	runsFinished       metric.Int64Counter
	runEvents          metric.Int64Counter
	analysisDuration   metric.Float64Histogram
	simulationDuration metric.Float64Histogram
}

var registered struct {
	sync.RWMutex
	value instruments
}

func normalize(config Config) (Config, error) {
	config.ServiceName = strings.TrimSpace(config.ServiceName)
	config.ServiceVersion = strings.TrimSpace(config.ServiceVersion)
	config.Environment = strings.TrimSpace(config.Environment)
	if config.ServiceName == "" {
		config.ServiceName = "flowverse-api"
	}
	if config.ServiceVersion == "" {
		config.ServiceVersion = "dev"
	}
	if config.Environment == "" {
		config.Environment = "development"
	}
	if config.SampleRatio < 0 || config.SampleRatio > 1 {
		return Config{}, fmt.Errorf("sample ratio must be between 0 and 1")
	}
	if config.MetricInterval == 0 {
		config.MetricInterval = 30 * time.Second
	}
	if config.MetricInterval < time.Second {
		return Config{}, fmt.Errorf("metric interval must be at least 1s")
	}
	return config, nil
}

// Setup installs OTLP/HTTP trace and metric providers. Exporters use the
// standard OTEL_EXPORTER_OTLP_* environment variables, keeping the service
// vendor-neutral and compatible with any OpenTelemetry Collector.
func Setup(ctx context.Context, config Config) (Shutdown, error) {
	config, err := normalize(config)
	if err != nil {
		return nil, err
	}
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	if !config.Enabled {
		return func(context.Context) error { return nil }, nil
	}

	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(config.ServiceName),
		semconv.ServiceVersion(config.ServiceVersion),
		semconv.DeploymentEnvironmentName(config.Environment),
	))
	if err != nil {
		return nil, fmt.Errorf("create telemetry resource: %w", err)
	}
	spanExporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
	}
	metricExporter, err := otlpmetrichttp.New(ctx)
	if err != nil {
		_ = spanExporter.Shutdown(ctx)
		return nil, fmt.Errorf("create OTLP metric exporter: %w", err)
	}

	traceProvider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(config.SampleRatio))),
		sdktrace.WithBatcher(spanExporter),
	)
	metricReader := sdkmetric.NewPeriodicReader(
		metricExporter,
		sdkmetric.WithInterval(config.MetricInterval),
	)
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(metricReader),
	)
	otel.SetTracerProvider(traceProvider)
	otel.SetMeterProvider(meterProvider)
	if err := registerInstruments(meterProvider.Meter(instrumentationScope)); err != nil {
		_ = traceProvider.Shutdown(ctx)
		_ = meterProvider.Shutdown(ctx)
		return nil, err
	}

	return func(shutdownContext context.Context) error {
		return errors.Join(
			meterProvider.Shutdown(shutdownContext),
			traceProvider.Shutdown(shutdownContext),
		)
	}, nil
}

func registerInstruments(meter metric.Meter) error {
	runsStarted, err := meter.Int64Counter("flowverse.runs.started", metric.WithUnit("{run}"))
	if err != nil {
		return fmt.Errorf("create runs started metric: %w", err)
	}
	runsFinished, err := meter.Int64Counter("flowverse.runs.finished", metric.WithUnit("{run}"))
	if err != nil {
		return fmt.Errorf("create runs finished metric: %w", err)
	}
	runEvents, err := meter.Int64Counter("flowverse.run.events", metric.WithUnit("{event}"))
	if err != nil {
		return fmt.Errorf("create run events metric: %w", err)
	}
	analysisDuration, err := meter.Float64Histogram("flowverse.analysis.duration", metric.WithUnit("ms"))
	if err != nil {
		return fmt.Errorf("create analysis duration metric: %w", err)
	}
	simulationDuration, err := meter.Float64Histogram("flowverse.simulation.duration", metric.WithUnit("ms"))
	if err != nil {
		return fmt.Errorf("create simulation duration metric: %w", err)
	}
	registered.Lock()
	registered.value = instruments{
		runsStarted: runsStarted, runsFinished: runsFinished, runEvents: runEvents,
		analysisDuration: analysisDuration, simulationDuration: simulationDuration,
	}
	registered.Unlock()
	return nil
}

func Tracer() trace.Tracer {
	return otel.Tracer(instrumentationScope)
}

func TraceID(ctx context.Context) string {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return ""
	}
	return spanContext.TraceID().String()
}

func RunStarted(ctx context.Context, target string) {
	registered.RLock()
	instrument := registered.value.runsStarted
	registered.RUnlock()
	if instrument != nil {
		instrument.Add(ctx, 1, metric.WithAttributes(attribute.String("flowverse.run.target", target)))
	}
}

func RunFinished(ctx context.Context, status string) {
	registered.RLock()
	instrument := registered.value.runsFinished
	registered.RUnlock()
	if instrument != nil {
		instrument.Add(ctx, 1, metric.WithAttributes(attribute.String("flowverse.run.status", status)))
	}
}

func RunEvent(ctx context.Context, eventType string) {
	registered.RLock()
	instrument := registered.value.runEvents
	registered.RUnlock()
	if instrument != nil {
		instrument.Add(ctx, 1, metric.WithAttributes(attribute.String("flowverse.event.type", eventType)))
	}
}

func AnalysisDuration(ctx context.Context, duration time.Duration) {
	registered.RLock()
	instrument := registered.value.analysisDuration
	registered.RUnlock()
	if instrument != nil {
		instrument.Record(ctx, float64(duration.Microseconds())/1000)
	}
}

func SimulationDuration(ctx context.Context, duration time.Duration, status string) {
	registered.RLock()
	instrument := registered.value.simulationDuration
	registered.RUnlock()
	if instrument != nil {
		instrument.Record(ctx, float64(duration.Microseconds())/1000,
			metric.WithAttributes(attribute.String("flowverse.run.status", status)))
	}
}
