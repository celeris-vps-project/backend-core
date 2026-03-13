package delayed

// This file contains interface placeholders for future message queue
// implementations. Only the type signatures are defined here — actual
// implementations live in separate files (or repositories) and are
// imported at the wiring layer (cmd/api/main.go).
//
// When a new broker is needed:
//  1. Create a new file (e.g. kafka_publisher.go) in this package.
//  2. Implement the Publisher and/or Consumer interfaces.
//  3. Wire it in cmd/api/main.go behind a config flag.

// ── Kafka ──────────────────────────────────────────────────────────────

// KafkaPublisher is a placeholder for a Kafka-backed Publisher implementation.
// It would use a Kafka producer to send delayed/scheduled messages.
//
// Typical usage with Kafka:
//   - Immediate messages: produce to a topic, consumer picks up ASAP.
//   - Delayed messages:   produce with a timestamp header; a delay-relay
//     service re-publishes when the delay expires, or use a Kafka Streams
//     processor with a punctuate timer.
//
// Required config: broker addresses, topic prefix, SASL credentials, TLS.
//
// type KafkaPublisher struct { ... }
// func NewKafkaPublisher(brokers []string, opts ...KafkaOption) (*KafkaPublisher, error)

// KafkaConsumer is a placeholder for a Kafka-backed Consumer implementation.
// It would use a Kafka consumer group to pull messages from subscribed topics.
//
// type KafkaConsumer struct { ... }
// func NewKafkaConsumer(brokers []string, groupID string, router *Router) (*KafkaConsumer, error)

// ── RabbitMQ ───────────────────────────────────────────────────────────

// RabbitMQPublisher is a placeholder for a RabbitMQ (AMQP) Publisher.
// RabbitMQ natively supports delayed messages via the
// rabbitmq_delayed_message_exchange plugin, or through per-message TTL
// combined with a dead-letter exchange.
//
// Required config: AMQP URI, exchange name, queue bindings.
//
// type RabbitMQPublisher struct { ... }
// func NewRabbitMQPublisher(amqpURI string, exchange string) (*RabbitMQPublisher, error)

// RabbitMQConsumer is a placeholder for a RabbitMQ Consumer.
//
// type RabbitMQConsumer struct { ... }
// func NewRabbitMQConsumer(amqpURI string, queueName string, router *Router) (*RabbitMQConsumer, error)

// ── NATS JetStream ─────────────────────────────────────────────────────

// NATSPublisher is a placeholder for a NATS JetStream Publisher.
// NATS JetStream provides at-least-once delivery with message replay,
// making it suitable for delayed event processing when combined with
// consumer-side delay logic or NAK-based redelivery.
//
// Required config: NATS URL, stream name, subject prefix, credentials.
//
// type NATSPublisher struct { ... }
// func NewNATSPublisher(natsURL string, streamName string) (*NATSPublisher, error)

// NATSConsumer is a placeholder for a NATS JetStream Consumer.
//
// type NATSConsumer struct { ... }
// func NewNATSConsumer(natsURL string, streamName string, router *Router) (*NATSConsumer, error)
