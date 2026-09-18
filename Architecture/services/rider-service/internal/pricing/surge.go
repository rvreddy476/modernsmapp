package pricing

// DefaultSurgeCapBPS is the ceiling on demand surge (MOPEDU_SURGE_CAP_BPS).
const DefaultSurgeCapBPS int64 = 5000

// DemandBPS maps the demand ratio (open ride requests in the last five
// minutes over online partners of the vehicle type; zero partners count as
// one) to a surge step, never above capBPS:
//
//	ratio < 1 -> 0, < 2 -> 2000, < 3 -> 4000, otherwise the cap.
func DemandBPS(requested, online int, capBPS int64) int64 {
	if capBPS < 0 {
		capBPS = 0
	}
	if requested <= 0 {
		return 0
	}
	if online < 1 {
		online = 1
	}
	var step int64
	switch {
	case requested < online: // ratio < 1
		step = 0
	case requested < 2*online: // ratio < 2
		step = 2000
	case requested < 3*online: // ratio < 3
		step = 4000
	default:
		step = capBPS
	}
	if step > capBPS {
		step = capBPS
	}
	return step
}

// EffectiveSurge combines the applicable window and the demand step: the
// larger of the two wins, never the sum. A tie goes to the window, so the
// quote names the peak window the customer can see.
func EffectiveSurge(window *Window, demandBPS int64) (bps int64, reason, windowName string) {
	extra := WindowExtraBPS(window)
	if demandBPS < 0 {
		demandBPS = 0
	}
	switch {
	case extra > 0 && extra >= demandBPS:
		return extra, SurgePeakHours, window.Name
	case demandBPS > 0:
		name := ""
		if window != nil {
			name = window.Name
		}
		return demandBPS, SurgeHighDemand, name
	}
	return 0, SurgeNone, ""
}
