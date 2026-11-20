package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	semconv "go.opentelemetry.io/otel/semconv/v1.7.0"
)

func Test_tracesExporter(t *testing.T) {
	enabled, disabled := true, false

	tests := []struct {
		name   string
		env    string
		otlp   bool
		stdout *bool
		want   string
	}{
		{name: "unset, config off", env: "", otlp: false, want: exporterNone},
		{name: "unset, config OTLP", env: "", otlp: true, want: exporterOtlp},
		{name: "unset, config stdout", env: "", otlp: false, stdout: &enabled, want: exporterConsole},
		{name: "env otlp beats config off (UpCloud)", env: "otlp", otlp: false, want: exporterOtlp},
		{name: "env none beats config OTLP", env: "none", otlp: true, want: exporterNone},
		{name: "env console", env: "console", otlp: true, stdout: &disabled, want: exporterConsole},
		{name: "env is trimmed and case-insensitive", env: " OTLP ", otlp: false, want: exporterOtlp},
		{name: "unknown env value: config decides", env: "zipkin", otlp: true, want: exporterOtlp},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(envTracesExporter, tt.env)
			config := &TracingConfig{OtlpExporterEnabled: tt.otlp, StdoutExporterEnabled: tt.stdout}
			config.setDefaults()
			require.Equal(t, tt.want, tracesExporter(config))
		})
	}
}

func Test_newResource(t *testing.T) {
	attr := func(t *testing.T, config *TracingConfig, key string) (string, bool) {
		config.setDefaults()
		res, err := newResource(context.Background(), config)
		require.NoError(t, err)
		for _, kv := range res.Attributes() {
			if string(kv.Key) == key {
				return kv.Value.AsString(), true
			}
		}
		return "", false
	}
	env := string(semconv.DeploymentEnvironmentKey)

	t.Run("from config", func(t *testing.T) {
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
		v, ok := attr(t, &TracingConfig{ServiceName: "svc", DeploymentEnvironment: "tandlakare-se-prod"}, env)
		require.True(t, ok)
		require.Equal(t, "tandlakare-se-prod", v)
	})

	t.Run("empty in config: not set", func(t *testing.T) {
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
		_, ok := attr(t, &TracingConfig{ServiceName: "svc"}, env)
		require.False(t, ok)
	})

	t.Run("environment wins", func(t *testing.T) {
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment=test")
		v, ok := attr(t, &TracingConfig{ServiceName: "svc", DeploymentEnvironment: "preview-287018"}, env)
		require.True(t, ok)
		require.Equal(t, "test", v)

		v, _ = attr(t, &TracingConfig{ServiceName: "svc"}, string(semconv.ServiceNameKey))
		require.Equal(t, "svc", v, "service name from the config stays")
	})
}

// On UpCloud, OtlpExporterEnabled is false (OnGCP), and the environment
// switches the OTLP exporter on and points it at the collector.
func Test_SetupTracing_OtlpFromEnvironment(t *testing.T) {
	var received atomic.Int32
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/traces" {
			received.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()

	t.Setenv(envTracesExporter, "otlp")
	t.Setenv(envOtlpEndpoint, collector.URL)

	tracerProvider, shutdownTracing := SetupTracing(
		context.Background(),
		&TracingConfig{
			ServiceName:         "serviceName",
			ServiceVersion:      "serviceVersion",
			OtlpExporterEnabled: false,
		},
	)
	_, span := tracerProvider.Tracer("test").Start(context.Background(), "span")
	span.End()
	shutdownTracing() // flushes

	require.Equal(t, int32(1), received.Load(), "the span should have reached the collector from OTEL_EXPORTER_OTLP_ENDPOINT")
}

// Without an exporter, spans are still created (and propagated), just not exported.
func Test_SetupTracing_NoExporter(t *testing.T) {
	t.Setenv(envTracesExporter, "none")

	tracerProvider, shutdownTracing := SetupTracing(
		context.Background(),
		&TracingConfig{ServiceName: "serviceName", OtlpExporterEnabled: true},
	)
	_, span := tracerProvider.Tracer("test").Start(context.Background(), "span")
	require.True(t, span.SpanContext().IsValid())
	span.End()
	shutdownTracing()
}
