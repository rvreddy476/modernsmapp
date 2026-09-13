package routing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// DefaultRoutesEndpoint is the Routes API computeRoutes method.
const DefaultRoutesEndpoint = "https://routes.googleapis.com/directions/v2:computeRoutes"

// DefaultTimeout bounds one computeRoutes call end to end (connect, request,
// response body).
const DefaultTimeout = 2 * time.Second

// RoutesFieldMask asks for exactly the two fields the ETA uses. The Routes API
// refuses a request without a field mask, and bills by what the mask asks for.
const RoutesFieldMask = "routes.duration,routes.distanceMeters"

// maxResponseBytes caps what is read from Google; a two-field answer is tiny.
const maxResponseBytes = 64 << 10

// GoogleOptions tune GoogleRoutes. Zero values take the defaults.
type GoogleOptions struct {
	// Endpoint replaces DefaultRoutesEndpoint (tests point it at httptest).
	Endpoint string
	// Timeout replaces DefaultTimeout.
	Timeout time.Duration
	// Client replaces the default client. The timeout is applied through the
	// request context, so a client without its own Timeout is still bounded.
	Client *http.Client
}

// GoogleRoutes is the Routes API computeRoutes client.
//
// The key travels only in the X-Goog-Api-Key header. It is never put in the
// URL (so it cannot reach proxy or access logs through the query string) and
// is redacted from every error this type returns, so a log line built from
// the error cannot carry it either.
type GoogleRoutes struct {
	key      string
	endpoint string
	timeout  time.Duration
	client   *http.Client
}

// NewGoogleRoutes builds the client for key.
func NewGoogleRoutes(key string, opts GoogleOptions) *GoogleRoutes {
	g := &GoogleRoutes{key: key, endpoint: opts.Endpoint, timeout: opts.Timeout, client: opts.Client}
	if g.endpoint == "" {
		g.endpoint = DefaultRoutesEndpoint
	}
	if g.timeout <= 0 {
		g.timeout = DefaultTimeout
	}
	if g.client == nil {
		g.client = &http.Client{}
	}
	return g
}

type computeRoutesLatLng struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type computeRoutesWaypoint struct {
	Location struct {
		LatLng computeRoutesLatLng `json:"latLng"`
	} `json:"location"`
}

// computeRoutesRequest is the documented body: origin and destination as
// location.latLng waypoints, travelMode TWO_WHEELER, routingPreference
// TRAFFIC_AWARE (allowed only for DRIVE and TWO_WHEELER). departureTime is
// omitted, which the API takes as "now".
type computeRoutesRequest struct {
	Origin            computeRoutesWaypoint `json:"origin"`
	Destination       computeRoutesWaypoint `json:"destination"`
	TravelMode        string                `json:"travelMode"`
	RoutingPreference string                `json:"routingPreference"`
}

// computeRoutesResponse is the masked answer: routes[].distanceMeters (int)
// and routes[].duration (a google.protobuf.Duration string such as "165s").
// No route at all comes back as an empty object.
type computeRoutesResponse struct {
	Routes []struct {
		DistanceMeters int     `json:"distanceMeters"`
		Duration       *string `json:"duration"`
	} `json:"routes"`
}

type googleErrorBody struct {
	Error struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	} `json:"error"`
}

func waypoint(p LatLng) computeRoutesWaypoint {
	var w computeRoutesWaypoint
	w.Location.LatLng = computeRoutesLatLng{Latitude: p.Lat, Longitude: p.Lng}
	return w
}

// Route calls computeRoutes once. Every failure is an *Error with a class.
func (g *GoogleRoutes) Route(ctx context.Context, from, to LatLng) (Route, error) {
	if !from.Valid() || !to.Valid() {
		return Route{}, &Error{Class: FailureRequest, msg: "coordinates out of range"}
	}
	body, err := json.Marshal(computeRoutesRequest{
		Origin: waypoint(from), Destination: waypoint(to),
		TravelMode: "TWO_WHEELER", RoutingPreference: "TRAFFIC_AWARE",
	})
	if err != nil {
		return Route{}, &Error{Class: FailureRequest, msg: "encode request"}
	}

	callCtx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, g.endpoint, bytes.NewReader(body))
	if err != nil {
		return Route{}, &Error{Class: FailureRequest, msg: g.redact(err.Error())}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Goog-Api-Key", g.key)
	req.Header.Set("X-Goog-FieldMask", RoutesFieldMask)

	resp, err := g.client.Do(req)
	if err != nil {
		return Route{}, g.callError(ctx, callCtx, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return Route{}, g.callError(ctx, callCtx, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := "non-2xx response"
		var ge googleErrorBody
		if json.Unmarshal(raw, &ge) == nil && (ge.Error.Status != "" || ge.Error.Message != "") {
			msg = strings.TrimSpace(ge.Error.Status + " " + ge.Error.Message)
		}
		return Route{}, &Error{Class: FailureHTTPStatus, StatusCode: resp.StatusCode, msg: g.redact(msg)}
	}

	var out computeRoutesResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return Route{}, &Error{Class: FailureDecode, msg: "response is not the computeRoutes shape"}
	}
	if len(out.Routes) == 0 || out.Routes[0].Duration == nil {
		return Route{}, &Error{Class: FailureEmptyRoute, msg: "no route between the points"}
	}
	first := out.Routes[0]
	d, err := parseProtoDuration(*first.Duration)
	if err != nil {
		return Route{}, &Error{Class: FailureDecode, msg: "duration is not a seconds string"}
	}
	if d < 0 || first.DistanceMeters < 0 {
		return Route{}, &Error{Class: FailureDecode, msg: "negative distance or duration"}
	}
	return Route{DistanceMeters: first.DistanceMeters, Duration: d, Source: SourceGoogle}, nil
}

// parseProtoDuration reads the JSON form of google.protobuf.Duration: decimal
// seconds with an "s" suffix ("165s", "3.5s").
func parseProtoDuration(s string) (time.Duration, error) {
	if !strings.HasSuffix(s, "s") || strings.ContainsAny(strings.TrimSuffix(s, "s"), "hmuµn") {
		return 0, errors.New("not a seconds duration")
	}
	return time.ParseDuration(s)
}

// callError classifies a failure of the round trip itself.
func (g *GoogleRoutes) callError(parent, callCtx context.Context, err error) error {
	switch {
	case parent.Err() != nil:
		return &Error{Class: FailureCanceled, msg: "caller context ended"}
	case errors.Is(callCtx.Err(), context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded):
		return &Error{Class: FailureTimeout, msg: "no answer within " + g.timeout.String()}
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return &Error{Class: FailureTimeout, msg: "no answer within " + g.timeout.String()}
	}
	return &Error{Class: FailureTransport, msg: g.redact(err.Error())}
}

// redact removes the key from s. The key is never put anywhere it could be
// echoed, but an error string is the one thing that reaches a log line, so it
// is scrubbed regardless.
func (g *GoogleRoutes) redact(s string) string {
	if g.key == "" {
		return s
	}
	return strings.ReplaceAll(s, g.key, "[redacted]")
}
