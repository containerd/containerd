# Tracing

containerd supports OpenTelemetry tracing since v1.6.0.
Tracing currently targets only gRPC calls.

## Sending traces from containerd daemon

containerd daemon can send traces to collection endpoints by configuring
[OpenTelemetry exporter environment variables](https://opentelemetry.io/docs/specs/otel/protocol/exporter/)
within the daemon's process space.

The following options are supported.

- `endpoint`: The address of a server that receives [OpenTelemetry Protocol](https://github.com/open-telemetry/opentelemetry-specification/blob/v1.8.0/specification/protocol/otlp.md).
- `protocol`: OpenTelemetry supports multiple protocols.
  The default value is "http/protobuf". "grpc" is also supported.
- `insecure`: Disable transport security when the protocol is "grpc". The default is false.
  "http/protobuf" always uses the schema provided by the endpoint and
  the value of this setting being ignored.

The sampling ratio and the service name on the traces can be configured by setting
[OpenTelemetry environment variables](https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/).

For example, if running containerd as a systemd service, add the environment variables to the service:

```text
[Service]
Environment="OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318"
Environment="OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf"
Environment="OTEL_SERVICE_NAME=containerd"
Environment="OTEL_TRACES_SAMPLER=traceidratio"
Environment="OTEL_TRACES_SAMPLER_ARG=1.0"
```

Or if running containerd from the command-line, set the environment variables before starting the daemon:

```bash
export OTEL_EXPORTER_OTLP_ENDPOINT="http://localhost:4318"
export OTEL_EXPORTER_OTLP_PROTOCOL="http/protobuf"
export OTEL_SERVICE_NAME="containerd"
export OTEL_TRACES_SAMPLER="traceidratio"
export OTEL_TRACES_SAMPLER_ARG=1.0
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
        grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
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
```
## Manual instrumentation

OpenTelemetry provides language specific [API](https://pkg.go.dev/go.opentelemetry.io/otel) libraries to instrument parts of your application that are not covered by automatic instrumentation.

In Containerd, a thin wrapper library defined in `tracing/tracing.go` provides additional functionality and makes it easier to use the OpenTelemetry API for manual instrumentation.

### Creating a new span

To create a new span, use the `tracing.StartSpan()` method. You should already have a global TracerProvider set by configuring `io.containerd.tracing.processor.v1.otlp` plugin, else this will only create an instance of a NoopSpan{}.

```go
func CreateContainer(ctx context.Context, r *runtime.CreateContainerRequest) error {
    ctx, span := tracing.StartSpan(ctx,
        tracing.Name(criSpanPrefix, "CreateContainer") // name of the span
        tracing.WithAttribute("sandbox.id",r.GetPodSandboxId(), //attributes to be added to the span
        )
	defer span.End() // end the span once the function returns
    ...
}
```
Mark the span complete at the end of workflow by calling `Span.End()`. In the above example, we use 'defer' to ensure that the span is properly closed and its duration is recorded.

### Adding Attributes to a span

You can add additional attributes to the span using `Span.SetAttributes()`. Attributes can be added during span creation (by passing `tracing.WithAttribute()` to tracing.StartSpan()) or at any other time during the lifecycle of a span before it has completed.

```go
func CreateContainer(ctx context.Context, r *runtime.CreateContainerRequest) error {
    ctx, span := tracing.StartSpan(ctx,
        tracing.Name(criSpanPrefix, "CreateContainer")
        tracing.WithAttribute("sandbox.id",r.GetPodSandboxId(),
        )
	defer span.End()
    ...
    containerId := util.GenerateID()
    containerName := makeContainerName(metadata, sandboxConfig.GetMetadata())

    //Add new attributes to the existing span
    span.SetAttributes(
		tracing.Attribute("container.id", containerId),
		tracing.Attribute("container.name", containerName),
	)
    ...
}
```
### Adding an Event to a span
Use `Span.AddEvent()` to add an event to an existing span. A [span event](https://opentelemetry.io/docs/instrumentation/go/manual/#events) is a specific occurrence within a span, such as the completion of an operation or the occurrence of an error. Span events can be used to provide additional information about the operation represented by the span, and can be used for debugging or performance analysis.

The below example shows how we can add an event to the span to mark the execution of an NRI hook.
```go
func CreateContainer(ctx context.Context, r *runtime.CreateContainerRequest) error {
    span := tracing.SpanFromContext(ctx) // get the current span from context
    ...
    ...
    if c.nri.isEnabled() {
        // Add an event to mark start of an NRI api call
        span.AddEvent("start NRI postCreateContainer request")

        err = c.nri.postCreateContainer(ctx, &sandbox, &container)
        if err != nil {
			log.G(ctx).WithError(err).Errorf("NRI post-create notification failed")
		}

        // Add an event to mark completion of the request
        span.AddEvent("finished NRI postCreateContainer request")
	}
    ...
    // You can also add additional attributes to an event
    span.AddEvent("container created",
		tracing.Attribute("container.create.duration", time.Since(start).String()),
	)

	return &runtime.CreateContainerResponse{ContainerId: id}, nil
}
```

### Recording errors in a span
You can  use `Span.RecordError()` to record an error as an exception span event for this span. The `RecordError` function does not automatically set a span’s status to Error, so if you wish to consider the span tracking a failed operation you should use `Span.SetStatus(err error)` to record the error and also set the span status to Error.

To record an error:
```go
span := tracing.SpanFromContext(ctx)
defer span.End()
...
err = c.nri.postCreateContainer(ctx, &sandbox, &container)
if err != nil {
    span.RecordError(err) //record error
	log.G(ctx).WithError(err).Errorf("NRI post-create notification failed")
}
```

To record an error and also set the span status:
```go
span := tracing.SpanFromContext(ctx)
defer span.End()
...
err = c.nri.postCreateContainer(ctx, &sandbox, &container)
if err != nil {
    span.SetStatus(err) //record error and set status
	log.G(ctx).WithError(err).Errorf("NRI post-create notification failed")
}
```

## Naming Convention

OpenTelemetry maintains a set of recommended [semantic conventions](https://opentelemetry.io/docs/reference/specification/overview/#semantic-conventions) for different types of telemetry data, such as traces and metrics, to help users of the OpenTelemetry libraries and tools to collect and use telemetry data in a consistent and interoperable way.

Manually instrumented spans in Containerd follow the conventions defined for [Spans](https://opentelemetry.io/docs/reference/specification/trace/semantic_conventions/) and [Attributes](https://opentelemetry.io/docs/reference/specification/common/attribute-naming/)

### Span Names
* Dot-separated notation.
* Span Names may include relative path to the package.
* Span Names should include a name that represents the specific component or service performing the operation.
* For example: "pkg.cri.sbserver.CreateContainer"
   * "pkg.cri.sbserver" - relative path to the package
   * "CreateContainer" - describes the operation that is traced

### Attribute Names
* Lower-case.
* Dot-separated notation.
* Use a namespace based representation.
* For example: "http.method.get" , http.method.post".
   * "http" - represents the general category of the attribute.
   * "method" - specific aspect or property of the attribute.
   * "get" - additional detail or context.