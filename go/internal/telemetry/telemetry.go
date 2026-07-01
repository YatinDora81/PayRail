package telemetry

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
)

func Init(ctx context.Context , serviceName string) (func(context.Context) error ,error){

	exp , err := otlptracegrpc.New(ctx)
	if err != nil{
		return nil , err
	}

	res , err := resource.New(ctx , 
		resource.WithFromEnv(),       // OTEL_SERVICE_NAME, OTEL_RESOURCE_ATTRIBUTES
		resource.WithTelemetrySDK(),  // sdk name/version/language
		resource.WithAttributes(semconv.ServiceNameKey.String(serviceName)),
	)

	if err != nil{
		return nil , err
	}

	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp, sdktrace.WithBatchTimeout(2*time.Second)),
	sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, // W3C traceparent
		propagation.Baggage{},
	))

	return tp.Shutdown, nil
}

func Tracer(name string) trace.Tracer{
	return otel.Tracer(name)
}

type ContextHandler struct{
	inner slog.Handler
}

func NewSlogHandler(base slog.Handler) slog.Handler{
	return &ContextHandler{
		inner: base,
	}
}

func (h *ContextHandler) Enabled(ctx context.Context,l slog.Level)bool{
	return h.inner.Enabled(ctx,l)
}

func (h *ContextHandler) Handle(ctx context.Context , r slog.Record)error{
	if sc := trace.SpanContextFromContext(ctx) ; sc.IsValid(){
		r.AddAttrs(
			slog.String("traceId", sc.TraceID().String()),
			slog.String("spanId", sc.SpanID().String()),
		)
	}
	return h.inner.Handle(ctx,r)
}

func (h *ContextHandler)WithAttrs(attrs []slog.Attr) slog.Handler{
	return &ContextHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h *ContextHandler) WithGroup(name string) slog.Handler{
	return &ContextHandler{inner: h.inner.WithGroup(name)}
}