package kafka

import (
	"context"

	kgo "github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("payrail/kafka")

type Writer struct {
	w *kgo.Writer
}

type headerCarrier []kgo.Header

type Message = kgo.Message

func (c *headerCarrier) Get(key string) string {
	for _, h := range *c {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}

func (c *headerCarrier) Set(key, val string) {
	for i := range *c {
		if (*c)[i].Key == key {
			(*c)[i].Value = []byte(val)
			return
		}
	}
	*c = append(*c, kgo.Header{Key: key, Value: []byte(val)}) // update-in-place or append — the carrier IS the message header slice
}

func (c *headerCarrier) Keys() []string {
	keys := make([]string, 0, len(*c))
	for _, h := range *c {
		keys = append(keys, h.Key)
	}
	return keys
}

func NewWriter(brokers []string) *Writer {
	return &Writer{w: &kgo.Writer{
		Addr:                   kgo.TCP(brokers...),
		Balancer:               &kgo.Hash{},    // same key → same partition = per-key ordering (the whole point)
		RequiredAcks:           kgo.RequireAll, // acks=all — an outbox row is only marked published on real durability
		AllowAutoTopicCreation: true,
	}}
}

func (p *Writer) Close() error {
	return p.w.Close()
}

func (p *Writer) Publish(ctx context.Context, topic, key string, value []byte) error {
	ctx, span := tracer.Start(ctx, "kafka.produce "+topic,
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			semconv.MessagingSystemKey.String("kafka"),
			semconv.MessagingDestinationName(topic),
		),
	)
	defer span.End()

	msg := kgo.Message{Topic: topic, Key: []byte(key), Value: value}
	otel.GetTextMapPropagator().Inject(ctx, (*headerCarrier)(&msg.Headers))

	if err := p.w.WriteMessages(ctx, msg); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	return nil
}
