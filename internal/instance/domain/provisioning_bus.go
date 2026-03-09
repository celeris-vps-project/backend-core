package domain

// ProvisionRequest carries all the information needed to provision a new
// instance on a host node. It is dispatched through the ProvisioningBus
// after the pending Instance record has been persisted.
type ProvisionRequest struct {
	InstanceID string
	CustomerID string
	OrderID    string
	NodeID     string
	Hostname   string
	Plan       string
	OS         string
	CPU        int
	MemoryMB   int
	DiskGB     int
}

// ProvisioningBus abstracts message delivery for provisioning requests.
//
// The interface is intentionally minimal so implementations can range from
// a direct synchronous call, to an in-memory channel-based queue, to an
// external message broker (RabbitMQ, NATS, Kafka, etc.).
//
// Implementations:
//   - ChannelProvisioningBus  — buffered Go channel (simple in-memory MQ)
//   - (future) RabbitMQProvisioningBus
//   - (future) NATSProvisioningBus
//   - NoopProvisioningBus     — does nothing (useful for tests)
type ProvisioningBus interface {
	// Dispatch enqueues a provisioning request for asynchronous processing.
	// Implementations may buffer the request (channel, MQ) or process it
	// inline (direct call). Returns an error only if the dispatch itself
	// fails (e.g. queue full, broker unreachable).
	Dispatch(req ProvisionRequest) error
}
