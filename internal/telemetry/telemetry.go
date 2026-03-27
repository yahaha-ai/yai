// Package telemetry provides OpenTelemetry instrumentation for yai.
package telemetry

import (
	"context"
	"log"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/yahaha-ai/yai/internal/config"
)

// Provider holds the OTel tracer and meter, and manages shutdown.
type Provider struct {
	tracerProvider *sdktrace.TracerProvider
	meterProvider  *sdkmetric.MeterProvider
	Tracer         trace.Tracer
	Meter          metric.Meter

	// Pre-created instruments
	RequestCounter   metric.Int64Counter
	RequestDuration  metric.Float64Histogram
	TokenUsage       metric.Int64Counter
	CostTotal        metric.Float64Counter
	FallbackTriggers metric.Int64Counter
	HealthStatus     metric.Int64UpDownCounter
}

// New initializes OTel with OTLP HTTP exporters. Returns nil if telemetry is disabled.
func New(ctx context.Context, cfg config.TelemetryConfig) (*Provider, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	serviceName := cfg.ServiceName
	if serviceName == "" {
		serviceName = "yai"
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(serviceName),
		),
	)
	if err != nil {
		return nil, err
	}

	// Trace exporter
	traceOpts := []otlptracehttp.Option{}
	if cfg.Endpoint != "" {
		traceOpts = append(traceOpts, otlptracehttp.WithEndpoint(stripScheme(cfg.Endpoint)))
		if isInsecure(cfg.Endpoint) {
			traceOpts = append(traceOpts, otlptracehttp.WithInsecure())
		}
	}
	traceExp, err := otlptracehttp.New(ctx, traceOpts...)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	// Metric exporter
	metricOpts := []otlpmetrichttp.Option{}
	if cfg.Endpoint != "" {
		metricOpts = append(metricOpts, otlpmetrichttp.WithEndpoint(stripScheme(cfg.Endpoint)))
		if isInsecure(cfg.Endpoint) {
			metricOpts = append(metricOpts, otlpmetrichttp.WithInsecure())
		}
	}
	metricExp, err := otlpmetrichttp.New(ctx, metricOpts...)
	if err != nil {
		tp.Shutdown(ctx)
		return nil, err
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp, sdkmetric.WithInterval(15*time.Second))),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(mp)

	tracer := tp.Tracer("yai")
	meter := mp.Meter("yai")

	p := &Provider{
		tracerProvider: tp,
		meterProvider:  mp,
		Tracer:         tracer,
		Meter:          meter,
	}

	// Create instruments
	p.RequestCounter, _ = meter.Int64Counter("yai.requests.total",
		metric.WithDescription("Total proxy requests"),
	)
	p.RequestDuration, _ = meter.Float64Histogram("yai.requests.duration",
		metric.WithDescription("Request duration in seconds"),
		metric.WithUnit("s"),
	)
	p.TokenUsage, _ = meter.Int64Counter("yai.tokens.usage",
		metric.WithDescription("Token usage"),
	)
	p.CostTotal, _ = meter.Float64Counter("yai.cost.total",
		metric.WithDescription("Accumulated cost in USD"),
		metric.WithUnit("USD"),
	)
	p.FallbackTriggers, _ = meter.Int64Counter("yai.fallback.triggers",
		metric.WithDescription("Number of fallback triggers"),
	)
	p.HealthStatus, _ = meter.Int64UpDownCounter("yai.health.status",
		metric.WithDescription("Provider health status (1=healthy, 0=unhealthy)"),
	)

	return p, nil
}

// Shutdown flushes and closes exporters.
func (p *Provider) Shutdown(ctx context.Context) {
	if p == nil {
		return
	}
	if err := p.tracerProvider.Shutdown(ctx); err != nil {
		log.Printf("otel trace shutdown: %v", err)
	}
	if err := p.meterProvider.Shutdown(ctx); err != nil {
		log.Printf("otel metric shutdown: %v", err)
	}
}

// RequestAttrs returns common attributes for a proxy request.
func RequestAttrs(provider, model, tokenName string, statusCode int) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("yai.provider", provider),
		attribute.String("yai.model", model),
		attribute.String("yai.token_name", tokenName),
		attribute.Int("http.status_code", statusCode),
	}
}

func stripScheme(endpoint string) string {
	for _, prefix := range []string{"http://", "https://"} {
		if len(endpoint) > len(prefix) && endpoint[:len(prefix)] == prefix {
			return endpoint[len(prefix):]
		}
	}
	return endpoint
}

func isInsecure(endpoint string) bool {
	return len(endpoint) >= 7 && endpoint[:7] == "http://"
}
