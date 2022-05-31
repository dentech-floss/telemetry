# telemetry

Setup [Opentelemetry](https://github.com/open-telemetry/opentelemetry-go) support for collecting and exporting traces to a backend using a batching strategy. The spans can be exported to either an [Opentelemetry Collector](https://github.com/open-telemetry/opentelemetry-collector) or just to stdout for local development usage. 

Note that both of these can't be enabled at the same time, first we check if the "OtlpExporterEnabled" flag is true and if that is not the case then we check if the "StdoutExporterEnabled" flag is true.

## Install

```
go get github.com/dentech-floss/telemetry@v0.1.0
```

## Usage

### Tracing

```go
package example

import (
    "github.com/dentech-floss/metadata/pkg/metadata"
    "github.com/dentech-floss/telemetry/pkg/telemetry"
    "github.com/dentech-floss/revision/pkg/revision"
)

func main() {

    ctx := context.Background()

    metadata := metadata.NewMetadata()

    shutdownTracing := telemetry.SetupTracing(
        ctx,
        &telemetry.TracingConfig{
            ServiceName:           revision.ServiceName,
            ServiceVersion:        revision.ServiceVersion,
            DeploymentEnvironment: metadata.ProjectID,
            OtlpExporterEnabled:   metadata.OnGCP,
            // OtlpCollectorHttpEndpoint: ..., // defaults to "opentelemetry-collector:80" if not set
            // OtlpCollectorTimeoutSecs: ...,  // default to 30 if not set
            // StdoutExporterEnabled: ...,     // if OtlpExporterEnabled is false, then you can enable this for stdout exporting
        },
    )
    defer shutdownTracing()
}
```

Now you can instrument different parts of your service that you want part of the tracing, like a gRPC server for example:

```go
package example

import (
    "google.golang.org/grpc"
    "go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
)

type Server struct {
    port       int
    grpcServer *grpc.Server
}

func NewServer(port int, patientGatewayServiceV1 *PatientGatewayServiceV1) *Server {

    grpcServer := grpc.NewServer(
        grpc.UnaryInterceptor(otelgrpc.UnaryServerInterceptor()),   // instrumentation
        grpc.StreamInterceptor(otelgrpc.StreamServerInterceptor()), // instrumentation
    )

    patientGatewayServiceV1.Register(grpcServer)

    return &Server{
        port:       port,
        grpcServer: grpcServer,
    }
}
```