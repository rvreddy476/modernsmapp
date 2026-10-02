// Pulse mechanics: the swipe-deck features added on top of the pilot (deck
// refill, rewind, Super Spark, …). Each one sits behind its own flag in
// MechanicsConfig, resolved from the environment at boot
// (http.ResolveMechanicsConfig): on by default only when ENV is local or dev,
// off everywhere else until the flag is set. The zero value is "everything
// off", which is what a Service built without SetMechanicsConfig gets.
//
// Every allowance and entitlement below is enforced here, on the server.
package service

// Daily deck allowances (cards acted on per store.DeckQuotaWindow).
const (
	DefaultDeckDailyLimitFree = 25
	DefaultDeckDailyLimitPass = 100
)

// MechanicsConfig is the per-mechanic flags and their limits.
type MechanicsConfig struct {
	// DeckRefill (DATING_DECK_REFILL_ENABLED): the deck refills as cards are
	// used, up to a daily allowance, and never repeats a card the user has
	// acted on.
	DeckRefill         bool
	DeckDailyLimitFree int
	DeckDailyLimitPass int
}

// DefaultMechanicsConfig is every mechanic off, with the default limits.
func DefaultMechanicsConfig() MechanicsConfig {
	return MechanicsConfig{
		DeckDailyLimitFree: DefaultDeckDailyLimitFree,
		DeckDailyLimitPass: DefaultDeckDailyLimitPass,
	}
}

// SetMechanicsConfig wires the flags and limits. Non-positive limits fall
// back to the defaults, so a flag can never mean "no limit".
func (s *Service) SetMechanicsConfig(cfg MechanicsConfig) {
	d := DefaultMechanicsConfig()
	if cfg.DeckDailyLimitFree <= 0 {
		cfg.DeckDailyLimitFree = d.DeckDailyLimitFree
	}
	if cfg.DeckDailyLimitPass <= 0 {
		cfg.DeckDailyLimitPass = d.DeckDailyLimitPass
	}
	s.mechanics = cfg
}

// Mechanics returns the active flags and limits.
func (s *Service) Mechanics() MechanicsConfig { return s.mechanics }
