package transport

import (
	"fmt"
	"strings"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl"
	"github.com/segmentio/kafka-go/sasl/plain"
	"github.com/segmentio/kafka-go/sasl/scram"
)

func KafkaDialerFromEnv() (*kafka.Dialer, error) {
	tlsConfig, err := tlsConfigFromEnv("KAFKA")
	if err != nil {
		return nil, err
	}
	mechanism, err := kafkaSASLMechanismFromEnv()
	if err != nil {
		return nil, err
	}
	return &kafka.Dialer{
		Timeout:       envDuration("KAFKA_DIAL_TIMEOUT", 10*time.Second),
		DualStack:     true,
		TLS:           tlsConfig,
		SASLMechanism: mechanism,
	}, nil
}

func kafkaSASLMechanismFromEnv() (sasl.Mechanism, error) {
	mechanism := strings.ToUpper(envString("KAFKA_SASL_MECHANISM"))
	if mechanism == "" || mechanism == "NONE" {
		return nil, nil
	}

	username := envString("KAFKA_SASL_USERNAME")
	password := envString("KAFKA_SASL_PASSWORD")
	if username == "" || password == "" {
		return nil, fmt.Errorf("KAFKA_SASL_USERNAME and KAFKA_SASL_PASSWORD are required when KAFKA_SASL_MECHANISM is set")
	}

	switch mechanism {
	case "PLAIN":
		return plain.Mechanism{
			Username: username,
			Password: password,
		}, nil
	case "SCRAM-SHA-256":
		return scram.Mechanism(scram.SHA256, username, password)
	case "SCRAM-SHA-512":
		return scram.Mechanism(scram.SHA512, username, password)
	default:
		return nil, fmt.Errorf("unsupported KAFKA_SASL_MECHANISM %q", mechanism)
	}
}
