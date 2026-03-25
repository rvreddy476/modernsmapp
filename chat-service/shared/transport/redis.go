package transport

import "github.com/redis/go-redis/v9"

func NewRedisClientFromEnv(addr string) (*redis.Client, error) {
	tlsConfig, err := tlsConfigFromEnv("REDIS")
	if err != nil {
		return nil, err
	}
	return redis.NewClient(&redis.Options{
		Addr:      addr,
		Username:  envString("REDIS_USERNAME"),
		Password:  envString("REDIS_PASSWORD"),
		DB:        envInt("REDIS_DB", 0),
		TLSConfig: tlsConfig,
	}), nil
}
