#!/bin/sh
set -eu

: "${REDIS_PASSWORD:?set REDIS_PASSWORD}"
: "${REDIS_TLS_CA_CERT_FILE:?set REDIS_TLS_CA_CERT_FILE}"

set -- redis-cli --tls --insecure --cacert "${REDIS_TLS_CA_CERT_FILE}" -a "${REDIS_PASSWORD}"

if [ -n "${REDIS_CLIENT_CERT_FILE:-}" ]; then
  set -- "$@" --cert "${REDIS_CLIENT_CERT_FILE}"
fi

if [ -n "${REDIS_CLIENT_KEY_FILE:-}" ]; then
  set -- "$@" --key "${REDIS_CLIENT_KEY_FILE}"
fi

exec "$@" ping
