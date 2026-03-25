#!/bin/bash
set -euo pipefail

BROKERS="${KAFKA_BROKERS:-redpanda:9092}"
PARTITIONS="${KAFKA_TOPIC_PARTITIONS:-3}"
REPLICAS="${KAFKA_TOPIC_REPLICAS:-1}"
APP_KAFKA_USER="${KAFKA_SASL_USERNAME:-}"
APP_KAFKA_PASS="${KAFKA_SASL_PASSWORD:-}"
APP_KAFKA_MECHANISM="${KAFKA_SASL_MECHANISM:-SCRAM-SHA-256}"
SUPERUSER_NAME="${REDPANDA_SUPERUSER_USERNAME:-}"
SUPERUSER_PASS="${REDPANDA_SUPERUSER_PASSWORD:-}"

export RPK_BROKERS="$BROKERS"

if [[ "${KAFKA_TLS_ENABLED:-false}" == "true" ]]; then
  export RPK_TLS_ENABLED="true"
  [[ -n "${KAFKA_TLS_SERVER_NAME:-}" ]] && export RPK_TLS_SERVER_NAME="${KAFKA_TLS_SERVER_NAME}"
  [[ -n "${KAFKA_CA_CERT_FILE:-}" ]] && export RPK_TLS_CA="${KAFKA_CA_CERT_FILE}"
  [[ -n "${KAFKA_CLIENT_CERT_FILE:-}" ]] && export RPK_TLS_CERT="${KAFKA_CLIENT_CERT_FILE}"
  [[ -n "${KAFKA_CLIENT_KEY_FILE:-}" ]] && export RPK_TLS_KEY="${KAFKA_CLIENT_KEY_FILE}"
fi

if [[ -n "${SUPERUSER_NAME}" && -n "${SUPERUSER_PASS}" ]]; then
  export RPK_USER="${SUPERUSER_NAME}"
  export RPK_PASS="${SUPERUSER_PASS}"
fi

if [[ -n "${REDPANDA_ADMIN_HOSTS:-}" ]]; then
  export RPK_ADMIN_HOSTS="${REDPANDA_ADMIN_HOSTS}"
fi

if [[ "${REDPANDA_ADMIN_TLS_ENABLED:-false}" == "true" ]]; then
  export RPK_ADMIN_TLS_ENABLED="true"
  [[ -n "${REDPANDA_ADMIN_TLS_SERVER_NAME:-}" ]] && export RPK_ADMIN_TLS_SERVER_NAME="${REDPANDA_ADMIN_TLS_SERVER_NAME}"
  [[ -n "${REDPANDA_ADMIN_TLS_CA_CERT_FILE:-}" ]] && export RPK_ADMIN_TLS_CA="${REDPANDA_ADMIN_TLS_CA_CERT_FILE}"
  [[ -n "${REDPANDA_ADMIN_TLS_CERT_FILE:-}" ]] && export RPK_ADMIN_TLS_CERT="${REDPANDA_ADMIN_TLS_CERT_FILE}"
  [[ -n "${REDPANDA_ADMIN_TLS_KEY_FILE:-}" ]] && export RPK_ADMIN_TLS_KEY="${REDPANDA_ADMIN_TLS_KEY_FILE}"
fi

until rpk cluster info >/dev/null 2>&1; do
  sleep 2
done

topics=(
  "social.events.v1"
  "social.events.v1.dlq"
  "chat.events.v1"
  "identity.events.v1"
  "call.lifecycle"
  "call.notifications"
  "call.analytics"
  "platform-events"
  "media.events"
  "atpost.channel.updates"
  "atpost.channel.notifications"
  "atpost.channel.feed-inject"
)

rpk cluster config set kafka_enable_authorization true >/dev/null 2>&1 || true
rpk cluster config set admin_api_require_auth true >/dev/null 2>&1 || true
rpk cluster config set http_authentication '["BASIC"]' >/dev/null 2>&1 || true

if [[ -n "${APP_KAFKA_USER}" && -n "${APP_KAFKA_PASS}" && "${APP_KAFKA_USER}" != "${SUPERUSER_NAME}" ]]; then
  rpk security user create "${APP_KAFKA_USER}" -p "${APP_KAFKA_PASS}" --mechanism "${APP_KAFKA_MECHANISM}" >/dev/null 2>&1 || \
    rpk security user update "${APP_KAFKA_USER}" --new-password "${APP_KAFKA_PASS}" --mechanism "${APP_KAFKA_MECHANISM}" >/dev/null 2>&1 || true

  rpk security acl create --allow-principal "User:${APP_KAFKA_USER}" --operation all --cluster >/dev/null 2>&1 || true
  rpk security acl create --allow-principal "User:${APP_KAFKA_USER}" --operation all --group "*" >/dev/null 2>&1 || true
  rpk security acl create --allow-principal "User:${APP_KAFKA_USER}" --operation all --transactional-id "*" >/dev/null 2>&1 || true
fi

for topic in "${topics[@]}"; do
  rpk topic create "$topic" -p "$PARTITIONS" -r "$REPLICAS" >/dev/null 2>&1 || true
  if [[ -n "${APP_KAFKA_USER}" && -n "${APP_KAFKA_PASS}" && "${APP_KAFKA_USER}" != "${SUPERUSER_NAME}" ]]; then
    rpk security acl create --allow-principal "User:${APP_KAFKA_USER}" --operation all --topic "$topic" >/dev/null 2>&1 || true
  fi
done

echo "Redpanda topics ready"
rpk topic list
