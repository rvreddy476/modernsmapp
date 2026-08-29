package events

import (
	"crypto/tls"
	"testing"

	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl/plain"
)

// CALL-LB-4: the DLQ writer must reach the SAME cluster the reader consumes
// from. On a TLS/SASL-secured broker a plaintext writer can never land the
// first poison record, and the fail-closed quarantine-before-commit loop
// would then stall that partition forever.
func TestDLQWriterCarriesTheReaderSecurityTransport(t *testing.T) {
	tlsConfig := &tls.Config{ServerName: "kafka.internal"}
	mech := plain.Mechanism{Username: "svc", Password: "secret"}
	dialer := &kafka.Dialer{TLS: tlsConfig, SASLMechanism: mech}

	dlq := newKafkaDLQ([]string{"kafka.internal:9093"}, "call.notifications", dialer)

	transport, ok := dlq.writer.Transport.(*kafka.Transport)
	if !ok || transport == nil {
		t.Fatal("secured dialer produced no writer transport (CALL-LB-4)")
	}
	if transport.TLS != tlsConfig {
		t.Fatal("DLQ writer dropped the reader's TLS config")
	}
	if transport.SASL != mech {
		t.Fatal("DLQ writer dropped the reader's SASL mechanism")
	}
}

func TestDLQWriterWithoutDialerUsesTheDefaultTransport(t *testing.T) {
	dlq := newKafkaDLQ([]string{"localhost:9092"}, "call.notifications", nil)
	if dlq.writer.Transport != nil {
		t.Fatalf("plain deployment grew an unexpected transport: %v", dlq.writer.Transport)
	}
}
