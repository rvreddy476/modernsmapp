//go:build integration

package postgres_test

import "os"

func getenv(key string) string { return os.Getenv(key) }
