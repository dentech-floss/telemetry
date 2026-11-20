package telemetry

import (
	"context"
	"os"
	"reflect"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"

	"go.opentelemetry.io/contrib/propagators/b3"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.7.0"
)

// Standard OpenTelemetry environment variables. When set they win over the
// config, so the deployment can switch tracing on or off without code
// changes. When none of them are set, the config decides as before.
//   - OTEL_TRACES_EXPORTER: "otlp", "console" (stdout) or "none". Unset or
//     another value: the config decides (OtlpExporterEnabled, StdoutExporterEnabled).
//   - OTEL_EXPORTER_OTLP_ENDPOINT / OTEL_EXPORTER_OTLP_TRACES_ENDPOINT: the
//     collector's URL (e.g. http://otel-collector.observability:4318), read by
//     the OTLP HTTP exporter itself. When set, OtlpCollectorHttpEndpoint is ignored.
//   - OTEL_RESOURCE_ATTRIBUTES: e.g. "deployment.environment=test", overrides
//     the resource attributes from the config.
const (
	envTracesExporter     = "OTEL_TRACES_EXPORTER"
	envOtlpEndpoint       = "OTEL_EXPORTER_OTLP_ENDPOINT"
	envOtlpTracesEndpoint = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"
)

const (
	exporterOtlp    = "otlp"
	exporterConsole = "console"
	exporterNone    = "none"
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
	Sampler    sdktrace.Sampler
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
	if interfaceIsNil(c.Propagator) {
		// If a propagator has not been provided then we default to W3C trace context/baggage
		c.Propagator = propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		)
	}
	if interfaceIsNil(c.Sampler) {
		// If a sampler has not been provided the we default to "always sample", not recommended for production...
		c.Sampler = sdktrace.AlwaysSample()
	}
}

func SetupTracing(ctx context.Context, config *TracingConfig) (*sdktrace.TracerProvider, func()) {
	config.setDefaults()

	res, err := newResource(ctx, config)
	if err != nil {
		panic(err)
	}

	var traceExporter sdktrace.SpanExporter
	switch tracesExporter(config) {
	case exporterOtlp:
		// Send the spans to a collector
		traceExporter, err = otlptrace.New(ctx, otlptracehttp.NewClient(otlpClientOptions(config)...))
	case exporterConsole:
		// Just pretty print the spans to stdout
		traceExporter, err = stdouttrace.New(stdouttrace.WithPrettyPrint())
	}
	if err != nil {
		panic(err)
	}

	tracerProviderOptions := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(config.Sampler),
	}
	if traceExporter != nil {
		// Register the trace exporter with a TracerProvider, using
		// a batch span processor to aggregate spans before export
		tracerProviderOptions = append(tracerProviderOptions,
			sdktrace.WithSpanProcessor(sdktrace.NewBatchSpanProcessor(traceExporter)),
		)
	}
	tracerProvider := sdktrace.NewTracerProvider(tracerProviderOptions...)
	otel.SetTracerProvider(tracerProvider)

	// Register the propagator globally
	otel.SetTextMapPropagator(config.Propagator)

	return tracerProvider, func() {
		tracerProvider.ForceFlush(ctx)
		tracerProvider.Shutdown(ctx)
	}
}

// newResource describes this application. OTEL_RESOURCE_ATTRIBUTES (and
// OTEL_SERVICE_NAME) override the attributes from the config.
func newResource(ctx context.Context, config *TracingConfig) (*resource.Resource, error) {
	attributes := []attribute.KeyValue{
		semconv.ServiceNameKey.String(config.ServiceName),
		semconv.ServiceVersionKey.String(config.ServiceVersion),
	}
	if config.DeploymentEnvironment != "" {
		attributes = append(attributes, semconv.DeploymentEnvironmentKey.String(config.DeploymentEnvironment))
	}
	return resource.New(
		ctx,
		resource.WithAttributes(attributes...),
		resource.WithFromEnv(), // last, so the environment wins
	)
}

// tracesExporter picks the exporter: OTEL_TRACES_EXPORTER if it names one we
// support, otherwise the config (OTLP first, then stdout).
func tracesExporter(config *TracingConfig) string {
	switch v := strings.ToLower(strings.TrimSpace(os.Getenv(envTracesExporter))); v {
	case exporterOtlp, exporterConsole, exporterNone:
		return v
	}
	if config.OtlpExporterEnabled {
		return exporterOtlp
	}
	if *config.StdoutExporterEnabled {
		return exporterConsole
	}
	return exporterNone
}

// otlpClientOptions: the endpoint from the environment when it's set there
// (the exporter reads it, with the scheme deciding TLS), otherwise the
// config's endpoint without TLS.
func otlpClientOptions(config *TracingConfig) []otlptracehttp.Option {
	options := []otlptracehttp.Option{
		otlptracehttp.WithTimeout(time.Duration(*config.OtlpCollectorTimeoutSecs) * time.Second),
	}
	if os.Getenv(envOtlpEndpoint) == "" && os.Getenv(envOtlpTracesEndpoint) == "" {
		options = append(options,
			otlptracehttp.WithEndpoint(*config.OtlpCollectorHttpEndpoint),
			otlptracehttp.WithInsecure(),
		)
	}
	return options
}

func interfaceIsNil(i interface{}) bool {
	if i == nil || (reflect.ValueOf(i).Kind() == reflect.Ptr && reflect.ValueOf(i).IsNil()) {
		return true
	}
	return false
}
