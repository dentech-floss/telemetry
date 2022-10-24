package telemetry

import (
	"context"
	"reflect"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"

	"go.opentelemetry.io/contrib/propagators/b3"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.7.0"
)

var defaultOtlpCollectorHttpEndpoint = "opentelemetry-collector:80"
var defaultOtlpCollectorTimeoutSecs = 30
var defaultStdoutExporterEnabled = false

// B3 propagator that can be used instead of the default W3C trace context/baggage
var B3_PROPAGATOR = b3.New()

type TracingConfig struct {
	ServiceName           string
	ServiceVersion        string
	DeploymentEnvironment string

	OtlpExporterEnabled       bool
	OtlpCollectorHttpEndpoint *string
	OtlpCollectorTimeoutSecs  *int

	StdoutExporterEnabled *bool

	Propagator propagation.TextMapPropagator
}

func (c *TracingConfig) setDefaults() {
	if c.OtlpCollectorHttpEndpoint == nil {
		c.OtlpCollectorHttpEndpoint = &defaultOtlpCollectorHttpEndpoint
	}
	if c.OtlpCollectorTimeoutSecs == nil {
		c.OtlpCollectorTimeoutSecs = &defaultOtlpCollectorTimeoutSecs
	}
	if c.StdoutExporterEnabled == nil {
		c.StdoutExporterEnabled = &defaultStdoutExporterEnabled
	}
	if c.Propagator == nil || (reflect.ValueOf(c.Propagator).Kind() == reflect.Ptr && reflect.ValueOf(c.Propagator).IsNil()) {
		// If a propagator has not been provided then we default to W3C trace context/baggage
		c.Propagator = propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		)
	}
}

func SetupTracing(ctx context.Context, config *TracingConfig) (*sdktrace.TracerProvider, func()) {
	config.setDefaults()

	// Create a resource describing this application
	res, err := resource.New(
		ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(config.ServiceName),
			semconv.ServiceVersionKey.String(config.ServiceVersion),
			semconv.DeploymentEnvironmentKey.String(config.DeploymentEnvironment),
		),
	)
	if err != nil {
		panic(err)
	}

	// Read this for an explanation to why we always sample:
	// https://anecdotes.dev/opentelemetry-on-google-cloud-unraveling-the-mystery-f61f044c18be
	sampler := sdktrace.AlwaysSample()

	var traceExporter sdktrace.SpanExporter
	if config.OtlpExporterEnabled {
		// Setup an otlp trace exporter that will send traces to a collector
		otlptraceClient := otlptracehttp.NewClient(
			otlptracehttp.WithEndpoint(*config.OtlpCollectorHttpEndpoint),
			otlptracehttp.WithInsecure(),
			otlptracehttp.WithTimeout(time.Duration(*config.OtlpCollectorTimeoutSecs)*time.Second),
		)
		traceExporter, err = otlptrace.New(ctx, otlptraceClient)
	} else if *config.StdoutExporterEnabled {
		// Setup a stdout trace exporter that just pretty prints
		traceExporter, err = stdouttrace.New(stdouttrace.WithPrettyPrint())
	}
	if err != nil {
		panic(err)
	}

	// Register the trace exporter with a TracerProvider, using
	// a batch span processor to aggregate spans before export
	bsp := sdktrace.NewBatchSpanProcessor(traceExporter)

	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
		sdktrace.WithSpanProcessor(bsp),
	)
	otel.SetTracerProvider(tracerProvider)

	// Register the propagator globally
	otel.SetTextMapPropagator(config.Propagator)

	return tracerProvider, func() {
		tracerProvider.ForceFlush(ctx)
		tracerProvider.Shutdown(ctx)
	}
}
