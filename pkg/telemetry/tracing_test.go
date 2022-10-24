package telemetry

import (
	"context"
	"reflect"
	"testing"

	"go.opentelemetry.io/otel"

	"github.com/stretchr/testify/require"
)

func Test_SetupTracing_Propagator(t *testing.T) {

	require := require.New(t)

	// We default to using W3C trace context/baggage here, so if we don't specify a propagator
	// then W3C shall be the default

	_, shutdownTracing := SetupTracing(
		context.Background(),
		&TracingConfig{
			ServiceName:           "serviceName",
			ServiceVersion:        "serviceVersion",
			DeploymentEnvironment: "projectID",
			OtlpExporterEnabled:   false,
			//Propagator:            B3_PROPAGATOR, // rely on the default which shall be W3C
		},
	)

	require.NotNil(otel.GetTextMapPropagator())

	propagatorType := reflect.TypeOf(otel.GetTextMapPropagator())
	require.Equal("go.opentelemetry.io/otel/propagation", propagatorType.PkgPath(), "unexpected propagator")
	require.Equal("propagation.compositeTextMapPropagator", propagatorType.String(), "unexpected propagator")

	shutdownTracing()

	// If one don't want to use W3C then it shall be possible to provide one. Here we switch to B3,
	// which the GCP infrastructure (gRPC API Gateway for example) does not understand and thus
	// will not tamper with (fudging up tracing where we do not want to use Cloud Trace)

	_, shutdownTracing = SetupTracing(
		context.Background(),
		&TracingConfig{
			ServiceName:           "serviceName",
			ServiceVersion:        "serviceVersion",
			DeploymentEnvironment: "projectID",
			OtlpExporterEnabled:   false,
			Propagator:            B3_PROPAGATOR, // this alternative is provided by this lib
		},
	)

	require.NotNil(otel.GetTextMapPropagator())

	propagatorType = reflect.TypeOf(otel.GetTextMapPropagator())
	require.Equal("go.opentelemetry.io/contrib/propagators/b3", propagatorType.PkgPath(), "unexpected propagator")
	require.Equal("b3.propagator", propagatorType.String(), "unexpected propagator")

	shutdownTracing()
}
