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

	// Rewind (DATING_REWIND_ENABLED): undo the last pass. Free users get
	// RewindDailyLimitFree per rolling 24 hours; a pass holder is not limited.
	Rewind               bool
	RewindDailyLimitFree int

	// SuperSpark (DATING_SUPER_SPARK_ENABLED): a stronger, limited spark.
	// The daily allowance is SuperSparkDailyLimitFree, or ...Pass for a pass
	// holder, per rolling 24 hours; beyond it a purchased pack is spent.
	SuperSpark               bool
	SuperSparkDailyLimitFree int
	SuperSparkDailyLimitPass int

	// LikedYouGate (DATING_LIKED_YOU_GATE_ENABLED): only a pass holder sees
	// who sparked them; everyone else gets the count and blurred cards.
	LikedYouGate bool
}

// DefaultMechanicsConfig is every mechanic off, with the default limits.
func DefaultMechanicsConfig() MechanicsConfig {
	return MechanicsConfig{
		DeckDailyLimitFree: DefaultDeckDailyLimitFree,
		DeckDailyLimitPass: DefaultDeckDailyLimitPass,

		RewindDailyLimitFree: DefaultRewindDailyLimitFree,

		SuperSparkDailyLimitFree: DefaultSuperSparkDailyLimitFree,
		SuperSparkDailyLimitPass: DefaultSuperSparkDailyLimitPass,
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
	if cfg.RewindDailyLimitFree <= 0 {
		cfg.RewindDailyLimitFree = d.RewindDailyLimitFree
	}
	if cfg.SuperSparkDailyLimitFree <= 0 {
		cfg.SuperSparkDailyLimitFree = d.SuperSparkDailyLimitFree
	}
	if cfg.SuperSparkDailyLimitPass <= 0 {
		cfg.SuperSparkDailyLimitPass = d.SuperSparkDailyLimitPass
	}
	s.mechanics = cfg
}

// Mechanics returns the active flags and limits.
func (s *Service) Mechanics() MechanicsConfig { return s.mechanics }
