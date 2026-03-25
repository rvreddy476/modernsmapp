#!/bin/sh
set -eu

: "${REDIS_PASSWORD:?set REDIS_PASSWORD}"
: "${REDIS_TLS_CERT_FILE:?set REDIS_TLS_CERT_FILE}"
: "${REDIS_TLS_KEY_FILE:?set REDIS_TLS_KEY_FILE}"
: "${REDIS_TLS_CA_CERT_FILE:?set REDIS_TLS_CA_CERT_FILE}"

cat > /tmp/redis.conf <<EOF
bind 0.0.0.0
protected-mode yes
port 0
tls-port 6379
tls-cert-file ${REDIS_TLS_CERT_FILE}
tls-key-file ${REDIS_TLS_KEY_FILE}
tls-ca-cert-file ${REDIS_TLS_CA_CERT_FILE}
tls-auth-clients ${REDIS_TLS_AUTH_CLIENTS:-no}
user default on >${REDIS_PASSWORD} ~* +@all
EOF

exec redis-server /tmp/redis.conf
