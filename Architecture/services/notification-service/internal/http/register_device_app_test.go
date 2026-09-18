package http

import (
	"testing"

	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/gin-gonic/gin/binding"
)

// The registration `app` oneof must name every install the store accepts —
// a client whose app the binding rejects gets a 400 and silently never
// receives push. Mopedu Captain (2026-09-18) registers as mopedu_captain.
func TestRegisterDeviceRequest_AcceptsEveryKnownApp(t *testing.T) {
	for _, app := range []string{"", postgres.AppMomentum, postgres.AppFeastKitchen, postgres.AppFeastRider, postgres.AppMopeduCaptain} {
		req := RegisterDeviceRequest{Platform: "android", PushToken: "tok", App: app}
		if err := binding.Validator.ValidateStruct(&req); err != nil {
			t.Errorf("app %q rejected by the binding: %v", app, err)
		}
	}
	for _, app := range []string{"captain", "mopedu", "MOPEDU_CAPTAIN", "feast"} {
		req := RegisterDeviceRequest{Platform: "android", PushToken: "tok", App: app}
		if err := binding.Validator.ValidateStruct(&req); err == nil {
			t.Errorf("unknown app %q accepted by the binding", app)
		}
	}
}
