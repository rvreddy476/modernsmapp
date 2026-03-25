#!/bin/bash
set -euo pipefail

: "${REDPANDA_SUPERUSER_USERNAME:?set REDPANDA_SUPERUSER_USERNAME}"
: "${REDPANDA_SUPERUSER_PASSWORD:?set REDPANDA_SUPERUSER_PASSWORD}"
: "${REDPANDA_TLS_CERT_FILE:?set REDPANDA_TLS_CERT_FILE}"
: "${REDPANDA_TLS_KEY_FILE:?set REDPANDA_TLS_KEY_FILE}"
: "${REDPANDA_TLS_CA_CERT_FILE:?set REDPANDA_TLS_CA_CERT_FILE}"

BROKER_HOST="${REDPANDA_ADVERTISED_KAFKA_ADDRESS:-redpanda}"
BROKER_PORT="${REDPANDA_KAFKA_TLS_PORT:-9093}"
ADMIN_HOST="${REDPANDA_ADVERTISED_ADMIN_ADDRESS:-redpanda}"
ADMIN_PORT="${REDPANDA_ADMIN_TLS_PORT:-9644}"
RPC_HOST="${REDPANDA_ADVERTISED_RPC_ADDRESS:-redpanda}"
RPC_PORT="${REDPANDA_RPC_PORT:-33145}"

export RP_BOOTSTRAP_USER="${REDPANDA_SUPERUSER_USERNAME}:${REDPANDA_SUPERUSER_PASSWORD}"

cat > /etc/redpanda/redpanda.yaml <<EOF
redpanda:
  data_directory: /var/lib/redpanda/data
  empty_seed_starts_cluster: true
  rpc_server:
    address: 0.0.0.0
    port: ${RPC_PORT}
  advertised_rpc_api:
    address: ${RPC_HOST}
    port: ${RPC_PORT}
  kafka_api:
    - address: 0.0.0.0
      port: ${BROKER_PORT}
      name: sasl_tls_listener
      authentication_method: sasl
  advertised_kafka_api:
    - address: ${BROKER_HOST}
      port: ${BROKER_PORT}
      name: sasl_tls_listener
  kafka_api_tls:
    - name: sasl_tls_listener
      enabled: true
      cert_file: ${REDPANDA_TLS_CERT_FILE}
      key_file: ${REDPANDA_TLS_KEY_FILE}
      truststore_file: ${REDPANDA_TLS_CA_CERT_FILE}
      require_client_auth: ${REDPANDA_KAFKA_REQUIRE_CLIENT_AUTH:-false}
  admin:
    - address: 0.0.0.0
      port: ${ADMIN_PORT}
      name: admin_tls_listener
  admin_api_tls:
    - name: admin_tls_listener
      enabled: true
      cert_file: ${REDPANDA_TLS_CERT_FILE}
      key_file: ${REDPANDA_TLS_KEY_FILE}
      truststore_file: ${REDPANDA_TLS_CA_CERT_FILE}
      require_client_auth: ${REDPANDA_ADMIN_REQUIRE_CLIENT_AUTH:-false}
  superusers:
    - ${REDPANDA_SUPERUSER_USERNAME}
  enable_sasl: true
rpk:
  kafka_api:
    brokers:
      - ${BROKER_HOST}:${BROKER_PORT}
    tls:
      enabled: true
      truststore_file: ${REDPANDA_TLS_CA_CERT_FILE}
  admin_api:
    addresses:
      - ${ADMIN_HOST}:${ADMIN_PORT}
    tls:
      enabled: true
      truststore_file: ${REDPANDA_TLS_CA_CERT_FILE}
EOF

exec rpk redpanda start \
  --config /etc/redpanda/redpanda.yaml \
  --check=false \
  --overprovisioned \
  --smp "${REDPANDA_SMP:-1}" \
  --memory "${REDPANDA_MEMORY:-1G}" \
  --reserve-memory "${REDPANDA_RESERVE_MEMORY:-0M}" \
  --node-id "${REDPANDA_NODE_ID:-0}"
