package http

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/atpost/media-service/internal/service"
)

// Every refusal of the client's own upload is a typed 4xx at confirm; only an
// unrecognised error is 500 INTERNAL_ERROR. The errors arrive wrapped, as the
// service returns them.
func TestConfirmUploadErrorMapping(t *testing.T) {
	wrap := func(sentinel error) error { return fmt.Errorf("%w: detail", sentinel) }
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"integrity", wrap(service.ErrUploadIntegrity), http.StatusConflict, "UPLOAD_INTEGRITY_ERROR"},
		{"unknown id", wrap(service.ErrUploadNotFound), http.StatusNotFound, "NOT_FOUND"},
		{"not the owner", wrap(service.ErrUploadForbidden), http.StatusForbidden, "FORBIDDEN"},
		{"object absent", wrap(service.ErrUploadObjectAbsent), http.StatusConflict, "UPLOAD_OBJECT_MISSING"},
		{"size mismatch", wrap(service.ErrUploadSizeMismatch), http.StatusUnprocessableEntity, "UPLOAD_SIZE_MISMATCH"},
		{"state", wrap(service.ErrUploadState), http.StatusConflict, "UPLOAD_STATE_CONFLICT"},
		{"voice duration", wrap(service.ErrUploadUnacceptable), http.StatusUnprocessableEntity, "UPLOAD_CONTENT_REJECTED"},
		{"outage", errors.New("dial tcp: connection refused"), http.StatusInternalServerError, "INTERNAL_ERROR"},
	}
	for _, c := range cases {
		status, code := confirmUploadError(c.err)
		if status != c.status || code != c.code {
			t.Errorf("%s: got %d %s, want %d %s", c.name, status, code, c.status, c.code)
		}
	}
}

// The service's real refusals, end to end through the mapping.
func TestConfirmUploadErrorMappingOfServiceRefusals(t *testing.T) {
	status, code := confirmUploadError(service.ValidateVoiceDuration(9999))
	if status != http.StatusUnprocessableEntity || code != "UPLOAD_CONTENT_REJECTED" {
		t.Errorf("over-long voice note: got %d %s", status, code)
	}
}
