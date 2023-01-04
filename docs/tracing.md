# Tracing

containerd supports OpenTelemetry tracing since v1.6.0.
Tracing currently targets only gRPC calls.

## Sending traces from containerd daemon

By configuring `io.containerd.tracing.processor.v1.otlp` plugin.
containerd daemon can send traces to the specified OpenTelemetry endpoint.

```toml
version = 2

[plugins."io.containerd.tracing.processor.v1.otlp"]
    endpoint = "http://localhost:4318"
```

The following options are supported.

- `endpoint`: The address of a server that receives [OpenTelemetry Protocol](https://github.com/open-telemetry/opentelemetry-specification/blob/v1.8.0/specification/protocol/otlp.md).
- `protocol`: OpenTelemetry supports multiple protocols.
  The default value is "http/protobuf". "grpc" is also supported.
- `insecure`: Disable transport security when the protocol is "grpc". The default is false.
  "http/protobuf" always uses the schema provided by the endpoint and
  the value of this setting being ignored.

The sampling ratio and the service name on the traces could be configured by
`io.containerd.internal.v1.tracing` plugin.

```toml
version = 2

[plugins."io.containerd.internal.v1.tracing"]
    sampling_ratio = 1.0
    service_name = "containerd"
```

## Sending traces from containerd client

By configuring its underlying gRPC client, containerd's Go client can send
traces to an OpenTelemetry endpoint.

Note that the Go client's methods and gRPC calls are not 1:1. Single method
call would issue multiple gRPC calls.

```go
func clientWithTrace() error {
	exp, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpoint("localhost:4318"),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		return err
	}

	res, err := resource.New(ctx, resource.WithAttributes(
		semconv.ServiceNameKey.String("CLIENT NAME"),
	))
	if err != nil {
		return err
	}

	provider := trace.NewTracerProvider(
		trace.WithSampler(trace.AlwaysSample()),
		trace.WithSpanProcessor(trace.NewSimpleSpanProcessor(exp)),
		trace.WithResource(res),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})

    ...

    dialOpts := []grpc.DialOption{
        grpc.WithTransportCredentials(insecure.NewCredentials()),
        grpc.WithStreamInterceptor(otelgrpc.StreamClientInterceptor()),
        grpc.WithUnaryInterceptor(otelgrpc.UnaryClientInterceptor()),
    }
    client, ctx, cancel, err := commands.NewClient(context, containerd.WithDialOpts(dialOpts))
    if err != nil {
        return err
    }
    defer cancel()

    ctx, span := tracing.StartSpan(ctx, "OPERATION NAME")
    defer span.End()
    ...
}
