package routing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/config"
)

var (
	// ErrRoutingUnconfigured is returned when production has no configured routing provider.
	ErrRoutingUnconfigured = errors.New("routing: real AWS routing provider unconfigured in production")
	// ErrInvalidCoordinates is returned when input coordinates are invalid.
	ErrInvalidCoordinates = errors.New("routing: invalid coordinates")
	// ErrRoutingFailed is returned when AWS Location Service returns error or invalid route.
	ErrRoutingFailed = errors.New("routing: AWS Location Service calculation failed")
)

// RouteResult captures the authoritative distance and duration from the routing provider.
type RouteResult struct {
	DistanceMeters  int     `json:"distance_meters"`
	DurationSeconds int     `json:"duration_seconds"`
	DistanceKM      float64 `json:"distance_km"`
	DurationMin     float64 `json:"duration_min"`
	ProviderVersion string  `json:"provider_version"`
}

// Calculator defines the routing abstraction.
type Calculator interface {
	CalculateRoute(ctx context.Context, pickupLat, pickupLng, dropLat, dropLng float64) (*RouteResult, error)
	ProviderVersion() string
}

// DeterministicCalculator is the test/dev router computing deterministic distances.
type DeterministicCalculator struct {
	WindingFactor float64
	AverageSpeed  float64
	Version       string
}

// NewDeterministicCalculator returns a deterministic calculator for tests and local development.
func NewDeterministicCalculator(winding, speed float64) *DeterministicCalculator {
	if winding <= 0 {
		winding = 1.25
	}
	if speed <= 0 {
		speed = 22.0
	}
	return &DeterministicCalculator{
		WindingFactor: winding,
		AverageSpeed:  speed,
		Version:       "deterministic-v1",
	}
}

func (d *DeterministicCalculator) ProviderVersion() string {
	if d.Version == "" {
		return "deterministic-v1"
	}
	return d.Version
}

// CalculateRoute computes distance and duration using Haversine with winding and speed.
func (d *DeterministicCalculator) CalculateRoute(_ context.Context, pLat, pLng, dLat, dLng float64) (*RouteResult, error) {
	if !validLatLng(pLat, pLng) || !validLatLng(dLat, dLng) {
		return nil, ErrInvalidCoordinates
	}

	straightKM := haversineKM(pLat, pLng, dLat, dLng)
	distKM := straightKM * d.WindingFactor
	durMin := (distKM / d.AverageSpeed) * 60.0

	return &RouteResult{
		DistanceMeters:  int(math.Round(distKM * 1000.0)),
		DurationSeconds: int(math.Round(durMin * 60.0)),
		DistanceKM:      distKM,
		DurationMin:     durMin,
		ProviderVersion: d.ProviderVersion(),
	}, nil
}

// AWSLocationCalculator routes via Amazon Location Service Routes API using IRSA credentials.
type AWSLocationCalculator struct {
	CalculatorName string
	Region         string
	Version        string
	httpClient     *http.Client
	signer         *v4.Signer
	cfg            *aws.Config
	isProd         bool
}

// NewAWSLocationCalculator creates an AWS Location Service route calculator.
func NewAWSLocationCalculator(ctx context.Context, calculatorName, region string) (*AWSLocationCalculator, error) {
	if calculatorName == "" {
		calculatorName = strings.TrimSpace(os.Getenv("AWS_LOCATION_ROUTE_CALCULATOR_NAME"))
	}
	if calculatorName == "" {
		calculatorName = strings.TrimSpace(os.Getenv("MOPEDU_ROUTING_CALCULATOR_NAME"))
	}
	if region == "" {
		region = strings.TrimSpace(os.Getenv("AWS_REGION"))
	}
	if region == "" {
		region = strings.TrimSpace(os.Getenv("AWS_DEFAULT_REGION"))
	}
	if region == "" {
		region = "ap-south-1"
	}

	isProd := isProductionEnv()
	if isProd && calculatorName == "" {
		return nil, ErrRoutingUnconfigured
	}

	var awsCfg *aws.Config
	if calculatorName != "" {
		c, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
		if err != nil && isProd {
			return nil, fmt.Errorf("failed to load AWS configuration for routing: %w", err)
		}
		awsCfg = &c
	}

	return &AWSLocationCalculator{
		CalculatorName: calculatorName,
		Region:         region,
		Version:        "aws-location-routes-v1",
		httpClient:     &http.Client{Timeout: 6 * time.Second},
		signer:         v4.NewSigner(),
		cfg:            awsCfg,
		isProd:         isProd,
	}, nil
}

func (a *AWSLocationCalculator) ProviderVersion() string {
	if a.Version == "" {
		return "aws-location-routes-v1"
	}
	return a.Version
}

// CalculateRoute calculates routes via AWS Location Service Routes API.
func (a *AWSLocationCalculator) CalculateRoute(ctx context.Context, pLat, pLng, dLat, dLng float64) (*RouteResult, error) {
	if !validLatLng(pLat, pLng) || !validLatLng(dLat, dLng) {
		return nil, ErrInvalidCoordinates
	}

	if a.CalculatorName != "" && a.cfg != nil {
		creds, err := a.cfg.Credentials.Retrieve(ctx)
		if err == nil {
			endpoint := fmt.Sprintf("https://routes.geo.%s.amazonaws.com/routes/v0/calculators/%s/calculate/route", a.Region, a.CalculatorName)
			reqBody, _ := json.Marshal(map[string]any{
				"DeparturePosition":   []float64{pLng, pLat},
				"DestinationPosition": []float64{dLng, dLat},
				"TravelMode":          "Car",
				"IncludeLegGeometry":  false,
			})
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
			if err == nil {
				req.Header.Set("Content-Type", "application/json")
				h := sha256.Sum256(reqBody)
				payloadHash := fmt.Sprintf("%x", h)
				if err := a.signer.SignHTTP(ctx, creds, req, payloadHash, "geo", a.Region, time.Now()); err == nil {
					resp, err := a.httpClient.Do(req)
					if err == nil {
						defer resp.Body.Close()
						if resp.StatusCode == http.StatusOK {
							var res struct {
								Summary struct {
									Distance        float64 `json:"Distance"`        // in Kilometers
									DurationSeconds float64 `json:"DurationSeconds"` // in Seconds
								} `json:"Summary"`
							}
							if err := json.NewDecoder(resp.Body).Decode(&res); err == nil && res.Summary.Distance > 0 {
								distKM := res.Summary.Distance
								durSec := int(math.Round(res.Summary.DurationSeconds))
								distMeters := int(math.Round(distKM * 1000.0))
								durMin := float64(durSec) / 60.0
								return &RouteResult{
									DistanceMeters:  distMeters,
									DurationSeconds: durSec,
									DistanceKM:      distKM,
									DurationMin:     durMin,
									ProviderVersion: a.ProviderVersion(),
								}, nil
							}
						}
					}
				}
			}
		}
		if a.isProd {
			return nil, ErrRoutingFailed
		}
	}

	if a.isProd {
		return nil, ErrRoutingUnconfigured
	}

	// Non-production fallback only when explicitly in non-prod environment
	det := NewDeterministicCalculator(1.25, 22.0)
	res, err := det.CalculateRoute(ctx, pLat, pLng, dLat, dLng)
	if err != nil {
		return nil, err
	}
	res.ProviderVersion = "deterministic-dev-fallback"
	return res, nil
}

func isProductionEnv() bool {
	for _, k := range []string{"APP_ENV", "ENVIRONMENT", "ENV"} {
		v := strings.ToLower(strings.TrimSpace(os.Getenv(k)))
		if v == "production" || v == "prod" || v == "staging" {
			return true
		}
	}
	return false
}

func validLatLng(lat, lng float64) bool {
	return lat >= -90 && lat <= 90 && lng >= -180 && lng <= 180 && (lat != 0 || lng != 0)
}

func haversineKM(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusKM = 6371.0
	dLat := (lat2 - lat1) * (math.Pi / 180.0)
	dLon := (lon2 - lon1) * (math.Pi / 180.0)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*(math.Pi/180.0))*math.Cos(lat2*(math.Pi/180.0))*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusKM * c
}
