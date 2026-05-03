module github.com/atpost/rider-service

go 1.24.0

require (
	github.com/atpost/shared v0.0.0
	github.com/gin-gonic/gin v1.9.1
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.5.5
	github.com/redis/go-redis/v9 v9.5.1
	github.com/segmentio/kafka-go v0.4.50
)

replace github.com/atpost/shared => ../../shared
