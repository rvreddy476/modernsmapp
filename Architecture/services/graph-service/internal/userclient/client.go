// Package userclient is a thin HTTP client to the Architecture user-service
// (the owner of the app.users table). graph-service calls EnsureUser before
// inserting a close_friends row — whose FK references app.users — so a stale
// user projection cannot fail the Trusted Circle action with a 500.
package userclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/shared/httpclient"
	"github.com/google/uuid"
)

// ErrUserNotFound means the user exists in neither user-service's projection
// nor the identity source — i.e. the user genuinely does not exist.
var ErrUserNotFound = errors.New("user not found")

// Client talks to user-service's /internal routes.
type Client struct {
	baseURL     string
	internalKey string
	http        *http.Client
}

// New builds a client. baseURL is e.g. http://user-service:8082. An empty
// baseURL yields a no-op client whose EnsureUser always returns nil, so the
// caller proceeds and relies on the FK constraint as the backstop.
func New(baseURL, internalKey string) *Client {
	return &Client{
		baseURL:     strings.TrimRight(baseURL, "/"),
		internalKey: internalKey,
		http:        httpclient.NewWithBreaker(5*time.Second, "graph->user"),
	}
}

// EnsureUser asks user-service to guarantee the user has a row in the
// app.users projection, repairing it from identity if a Kafka event was lost.
// Returns nil on success (row present/repaired), ErrUserNotFound when the user
// does not exist anywhere, and a transport error otherwise.
func (c *Client) EnsureUser(ctx context.Context, id uuid.UUID) error {
	if c == nil || c.baseURL == "" {
		return nil // not configured — caller proceeds; FK is the backstop
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/internal/users/%s/ensure", c.baseURL, id), nil)
	if err != nil {
		return err
	}
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusNotFound:
		return ErrUserNotFound
	default:
		return fmt.Errorf("user-service ensure %s: status %d", id, resp.StatusCode)
	}
}
