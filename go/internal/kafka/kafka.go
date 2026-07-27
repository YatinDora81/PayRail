package kafka

import (
	"context"
	"errors"
	"log/slog"
	"time"

	kgo "github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
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

type Reader struct {
	r *kgo.Reader
}

func NewReader(brokers []string, groupId, topic string) *Reader {
	return &Reader{
		r: kgo.NewReader(kgo.ReaderConfig{
			Brokers:  brokers,
			GroupID:  groupId,
			Topic:    topic,
			MinBytes: 1,
			MaxBytes: 10e6,
		})}
}

func (c *Reader) Close() error {
	return c.r.Close()
}

func (c *Reader) Run(ctx context.Context, logger *slog.Logger, handle func(context.Context, Message) error) error {
	for {
		m, err := c.r.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}

		msgCtx := otel.GetTextMapPropagator().Extract(ctx, propagation.TextMapCarrier((*headerCarrier)(&m.Headers)))
		msgCtx, span := tracer.Start(msgCtx, "kafka.consume "+m.Topic,
			trace.WithSpanKind(trace.SpanKindConsumer),
			trace.WithAttributes(
				semconv.MessagingSystemKey.String("kafka"),
				semconv.MessagingDestinationName(m.Topic),
			),
		)

		backoff := time.Second
		for attempt := 1; ; attempt++ {
			err := handle(msgCtx, m)
			if err == nil {
				break
			}
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			logger.ErrorContext(msgCtx, "kafka handler failed — will retry, NOT committing",
				"topic", m.Topic, "partition", m.Partition, "offset", m.Offset,
				"attempt", attempt, "err", err)
			select {
			case <-ctx.Done():
				span.End()
				return nil // uncommitted — redelivered to the next consumer
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
		}
		span.End()

		if err := c.r.CommitMessages(ctx, m); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}

	}
}
