// Client config (mechanic M18): switches the apps read once and act on
// locally — today only screen protection. GET /v1/dating/client-config.
//
// ScreenProtection (DATING_SCREEN_PROTECTION_ENABLED): the Android app
// blocks screenshots and screen recording on the dating screens that show
// other people (deck, profiles, picks, liked you, matches), not in chat, so
// a conversation can still be captured to report it. The web cannot block
// screenshots and ignores it. It protects people's photos from casual
// copying; it is not a security boundary.
package service

// ClientConfig is GET /v1/dating/client-config.
type ClientConfig struct {
	ScreenProtection bool `json:"screen_protection"`
}

// ClientConfig returns the switches for the apps.
func (s *Service) ClientConfig() ClientConfig {
	return ClientConfig{ScreenProtection: s.mechanics.ScreenProtection}
}
