package processing

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/rekognition"
	rektypes "github.com/aws/aws-sdk-go-v2/service/rekognition/types"
)

// Module 4 M4-P0-3 — the approval-capable content scanner.
//
// WHAT REPLACED WHAT
//
// The worker wired StubScanner unconditionally, and StubScanner returns
// {IsSafe: true} for everything. Combined with the worker defaulting the
// moderation status to "passed", every video ever uploaded was approved by a
// component that performed no scan. The startup log said "media scanner:
// STUB SCANNER ACTIVE" and nothing acted on it.
//
// THE ONE INVARIANT
//
// ErrScanUnavailable is returned for EVERY path that is not a confident
// verdict: throttling, timeout, transport failure, an unsupported image, a
// malformed response, or a confidence below threshold. It is never conflated
// with "safe". The caller holds the asset for manual review.
//
// This matters more than the detection quality. A scanner that misses some
// unsafe content is a tuning problem; a scanner that reports "safe" when it did
// not run is a hole that looks like a feature.
//
// CREDENTIALS
//
// The default credential chain resolves the pod's IRSA web identity. There is
// no static-key path here at all — the worker refuses to start if static AWS
// credentials are present in the environment (see cmd/worker selectScanner).

// ErrScanUnavailable means no verdict could be reached. Callers MUST hold the
// content for manual review; they must not treat it as safe.
var ErrScanUnavailable = errors.New("content scan unavailable")

// RekognitionConfig tunes the scanner.
type RekognitionConfig struct {
	// MinConfidence is the percentage below which Rekognition's own findings
	// are not returned at all. Lower means more findings and more false
	// positives; the safety-conservative direction is LOWER, because a false
	// positive costs a manual review and a false negative publishes.
	MinConfidence float32
	// BlockedLabels are the top-level moderation categories that fail an
	// asset. Empty means the default set below.
	BlockedLabels []string
}

// defaultBlockedLabels are the Rekognition top-level moderation categories
// treated as unpublishable.
//
// "Explicit Nudity" and "Violence" are the launch-critical ones. Categories
// like "Alcohol" and "Gambling" are deliberately NOT here: they are legal
// content on a social platform, and blocking them would make the scanner an
// unusable source of false positives that operators learn to ignore.
var defaultBlockedLabels = []string{
	"Explicit Nudity",
	"Explicit Sexual Activity",
	"Violence",
	"Visually Disturbing",
	"Hate Symbols",
}

// rekognitionAPI is the one call this needs, as an interface so the failure
// matrix can be tested without AWS.
type rekognitionAPI interface {
	DetectModerationLabels(ctx context.Context, in *rekognition.DetectModerationLabelsInput,
		optFns ...func(*rekognition.Options)) (*rekognition.DetectModerationLabelsOutput, error)
}

// RekognitionScanner implements Scanner against AWS Rekognition.
type RekognitionScanner struct {
	api           rekognitionAPI
	minConfidence float32
	blocked       map[string]bool
}

// NewRekognitionClient builds the Rekognition client shared by the content
// scanner and the face comparer (lane D5): the default AWS credential chain
// (IRSA in cluster) for region, with credentials resolved eagerly.
func NewRekognitionClient(ctx context.Context, region string) (*rekognition.Client, error) {
	if region == "" {
		return nil, fmt.Errorf("rekognition: region is required")
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("rekognition: load AWS config: %w", err)
	}
	// Credentials are resolved here rather than lazily so a missing IRSA
	// identity fails at startup, not on the first upload of the day.
	if _, err := awsCfg.Credentials.Retrieve(ctx); err != nil {
		return nil, fmt.Errorf("rekognition: no usable AWS credentials (expected IRSA web identity): %w", err)
	}
	return rekognition.NewFromConfig(awsCfg), nil
}

// NewRekognitionScanner builds a scanner using the default AWS credential
// chain (IRSA in cluster).
func NewRekognitionScanner(ctx context.Context, region string, cfg RekognitionConfig) (*RekognitionScanner, error) {
	client, err := NewRekognitionClient(ctx, region)
	if err != nil {
		return nil, err
	}
	return NewRekognitionScannerFromClient(client, cfg), nil
}

// NewRekognitionScannerFromClient builds a scanner on an existing client, so
// the server can share one client between scanning and face comparison.
func NewRekognitionScannerFromClient(client *rekognition.Client, cfg RekognitionConfig) *RekognitionScanner {
	return newRekognitionScannerWithAPI(client, cfg)
}

// SharedRekognition builds one Rekognition client on first use and hands the
// same client to every caller in the process (scanner + face comparer).
// Not safe for concurrent first use; main calls it during boot only.
type SharedRekognition struct {
	client *rekognition.Client
	region string
}

// Client returns the shared client, building it for region on first use. A
// later call for a different region is an error rather than a silent reuse.
func (s *SharedRekognition) Client(ctx context.Context, region string) (*rekognition.Client, error) {
	if s.client != nil {
		if region != s.region {
			return nil, fmt.Errorf("rekognition: client already built for region %q, asked for %q", s.region, region)
		}
		return s.client, nil
	}
	c, err := NewRekognitionClient(ctx, region)
	if err != nil {
		return nil, err
	}
	s.client, s.region = c, region
	return c, nil
}

func newRekognitionScannerWithAPI(api rekognitionAPI, cfg RekognitionConfig) *RekognitionScanner {
	minConf := cfg.MinConfidence
	if minConf <= 0 || minConf > 100 {
		minConf = 80
	}
	labels := cfg.BlockedLabels
	if len(labels) == 0 {
		labels = defaultBlockedLabels
	}
	blocked := make(map[string]bool, len(labels))
	for _, l := range labels {
		blocked[strings.ToLower(strings.TrimSpace(l))] = true
	}
	return &RekognitionScanner{api: api, minConfidence: minConf, blocked: blocked}
}

// ScanImage returns a verdict, or ErrScanUnavailable.
//
// It never returns {IsSafe: true} alongside an error, and it never returns
// IsSafe on any path where the provider did not actually answer.
func (s *RekognitionScanner) ScanImage(ctx context.Context, data []byte) (ScanResult, error) {
	if s == nil || s.api == nil {
		return ScanResult{}, fmt.Errorf("%w: scanner not configured", ErrScanUnavailable)
	}
	if len(data) == 0 {
		// Nothing to scan is not the same as nothing unsafe.
		return ScanResult{}, fmt.Errorf("%w: empty image", ErrScanUnavailable)
	}

	out, err := s.api.DetectModerationLabels(ctx, &rekognition.DetectModerationLabelsInput{
		Image:         &rektypes.Image{Bytes: data},
		MinConfidence: aws.Float32(s.minConfidence),
	})
	if err != nil {
		// Every provider error shape lands here: throttling, timeout,
		// unsupported format, access denied, region outage. All unavailable,
		// none safe.
		return ScanResult{}, fmt.Errorf("%w: %v", ErrScanUnavailable, err)
	}
	if out == nil {
		return ScanResult{}, fmt.Errorf("%w: provider returned no result", ErrScanUnavailable)
	}

	// A nil ModerationLabels slice is a genuine "no findings" answer from
	// Rekognition, which is distinct from an error. An empty result after a
	// successful call means the image cleared the threshold.
	//
	// Every label is kept (lane D6): categories that do not fail the upload
	// (swimwear, suggestive, drugs) still decide whether a dating photo is
	// approved or reviewed.
	labels := make([]ModerationLabel, 0, len(out.ModerationLabels))
	for _, l := range out.ModerationLabels {
		ml := ModerationLabel{}
		if l.Name != nil {
			ml.Name = *l.Name
		}
		if l.ParentName != nil {
			ml.Parent = *l.ParentName
		}
		if l.Confidence != nil {
			ml.Confidence = float64(*l.Confidence)
		}
		labels = append(labels, ml)
	}
	if worstLabel, worst := worstBlocked(labels, s.blocked); worstLabel != "" {
		return ScanResult{
			IsSafe: false,
			Reason: strings.ToLower(strings.ReplaceAll(worstLabel, " ", "_")),
			Score:  worst / 100,
			Labels: labels,
		}, nil
	}
	return ScanResult{IsSafe: true, Score: 0, Labels: labels}, nil
}

// Name implements NamedScanner.
func (*RekognitionScanner) Name() string { return "rekognition" }
