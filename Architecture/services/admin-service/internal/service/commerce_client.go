package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// CommerceClient proxies admin actions to commerce-service internal endpoints.
type CommerceClient struct {
	baseURL     string
	internalKey string
	httpClient  *http.Client
}

func NewCommerceClient(baseURL, internalKey string) *CommerceClient {
	return &CommerceClient{
		baseURL:     baseURL,
		internalKey: internalKey,
		httpClient:  &http.Client{},
	}
}

func (c *CommerceClient) do(ctx context.Context, method, path string, body any) ([]byte, int, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("marshal: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, 0, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", c.internalKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	return data, resp.StatusCode, err
}

func (c *CommerceClient) ListSellerQueue(ctx context.Context, limit, offset int) ([]byte, int, error) {
	return c.do(ctx, http.MethodGet,
		fmt.Sprintf("/v1/commerce/internal/sellers/queue?limit=%d&offset=%d", limit, offset), nil)
}

func (c *CommerceClient) GetSeller(ctx context.Context, sellerID string) ([]byte, int, error) {
	return c.do(ctx, http.MethodGet, "/v1/commerce/internal/sellers/"+sellerID, nil)
}

type adminActionPayload struct {
	Reason  string `json:"reason,omitempty"`
	Notes   string `json:"notes,omitempty"`
	Changes string `json:"changes,omitempty"`
}

func (c *CommerceClient) ApproveSeller(ctx context.Context, sellerID, actorID, notes string) (int, error) {
	_, status, err := c.do(ctx, http.MethodPost,
		"/v1/commerce/internal/sellers/"+sellerID+"/approve",
		adminActionPayload{Notes: notes})
	return status, err
}

func (c *CommerceClient) RejectSeller(ctx context.Context, sellerID, actorID, reason, notes string) (int, error) {
	_, status, err := c.do(ctx, http.MethodPost,
		"/v1/commerce/internal/sellers/"+sellerID+"/reject",
		adminActionPayload{Reason: reason, Notes: notes})
	return status, err
}

func (c *CommerceClient) RequestSellerChanges(ctx context.Context, sellerID, actorID, changes, notes string) (int, error) {
	_, status, err := c.do(ctx, http.MethodPost,
		"/v1/commerce/internal/sellers/"+sellerID+"/request-changes",
		adminActionPayload{Changes: changes, Notes: notes})
	return status, err
}

func (c *CommerceClient) SuspendSeller(ctx context.Context, sellerID, actorID, reason, notes string) (int, error) {
	_, status, err := c.do(ctx, http.MethodPost,
		"/v1/commerce/internal/sellers/"+sellerID+"/suspend",
		adminActionPayload{Reason: reason, Notes: notes})
	return status, err
}

func (c *CommerceClient) ListProductQueue(ctx context.Context, limit, offset int) ([]byte, int, error) {
	return c.do(ctx, http.MethodGet,
		fmt.Sprintf("/v1/commerce/internal/products/queue?limit=%d&offset=%d", limit, offset), nil)
}

func (c *CommerceClient) ApproveProduct(ctx context.Context, productID, actorID, notes string) (int, error) {
	_, status, err := c.do(ctx, http.MethodPost,
		"/v1/commerce/internal/products/"+productID+"/approve",
		adminActionPayload{Notes: notes})
	return status, err
}

func (c *CommerceClient) RejectProduct(ctx context.Context, productID, actorID, reason string) (int, error) {
	_, status, err := c.do(ctx, http.MethodPost,
		"/v1/commerce/internal/products/"+productID+"/reject",
		adminActionPayload{Reason: reason})
	return status, err
}
