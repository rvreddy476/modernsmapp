module github.com/atpost/doorstep-service

go 1.24.0

require (
	github.com/atpost/shared v0.0.0
	github.com/gin-gonic/gin v1.9.1
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.5.5
)

replace github.com/atpost/shared => ../../shared
