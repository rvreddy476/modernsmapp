module github.com/facebook-like/payments-service

go 1.22

require (
	github.com/facebook-like/shared v0.0.0
	github.com/gin-gonic/gin v1.9.1
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.5.5
	github.com/segmentio/kafka-go v0.4.47
)

replace github.com/facebook-like/shared => ../../shared
