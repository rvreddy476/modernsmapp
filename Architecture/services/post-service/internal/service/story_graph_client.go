package service

import (
	"context"
	"net/http"

	"github.com/atpost/post-service/pkg/graphclient"
)

// HTTPGraphRelationships adapts the exported, contract-testable graph client
// to the story policy's internal relationship type. Production and the
// handler-to-client contract test now execute the same decoder; keeping a
// second HTTP implementation here is the drift that caused Module 4 LB-1.
type HTTPGraphRelationships struct {
	client *graphclient.Client
}

func NewHTTPGraphRelationships(baseURL, internalKey string, httpClient *http.Client) *HTTPGraphRelationships {
	return &HTTPGraphRelationships{client: graphclient.New(baseURL, internalKey, httpClient)}
}

func (g *HTTPGraphRelationships) Following(ctx context.Context, viewerID string) ([]string, error) {
	return g.client.Following(ctx, viewerID)
}

func (g *HTTPGraphRelationships) RelationshipBatch(ctx context.Context, viewerID string, targetIDs []string) (map[string]ViewerRelationship, error) {
	raw, err := g.client.RelationshipBatch(ctx, viewerID, targetIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[string]ViewerRelationship, len(raw))
	for id, r := range raw {
		out[id] = ViewerRelationship{
			Follows:                     r.Follows,
			Blocked:                     r.Blocked,
			BlockedBy:                   r.BlockedBy,
			Muted:                       r.IsMuted,
			ViewerIsCloseFriendOfTarget: r.ViewerIsCloseFriendOfTarget,
		}
	}
	return out, nil
}
