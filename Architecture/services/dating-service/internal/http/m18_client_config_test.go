package http

import (
	"net/http"
	"testing"

	"github.com/atpost/dating-service/internal/service"
	"github.com/google/uuid"
)

// Mechanic M18 — client config (DATING_SCREEN_PROTECTION_ENABLED).

func TestM18ClientConfigContracts(t *testing.T) {
	on := newM1Deck(t, service.MechanicsConfig{ScreenProtection: true})
	assertContract(t, contractDo(on.env.r, http.MethodGet, "/v1/dating/client-config", ``, on.viewer),
		http.StatusOK, "client_config_get_200", map[uuid.UUID]string{on.viewer: "<viewer>"})
	off := newM1Deck(t, service.MechanicsConfig{})
	assertContract(t, contractDo(off.env.r, http.MethodGet, "/v1/dating/client-config", ``, off.viewer),
		http.StatusOK, "client_config_get_200_off", map[uuid.UUID]string{off.viewer: "<viewer>"})
}
