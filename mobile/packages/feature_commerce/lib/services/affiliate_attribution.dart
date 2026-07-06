// Affiliate-attribution session glue for mobile.
//
// Mirrors postbook-ui/src/hooks/useAffiliateAttribution.ts. Captures
// the ?via=<affiliate_code> query string from the affiliate-redirect
// landing, persists it for the buying session, and clears on
// successful checkout. Last-touch wins (industry convention).
//
// Storage tier
//   In-memory only. Sessions are short (open app → tap → buy in
//   minutes); the attribution shouldn't outlive the process. If the
//   user backgrounds the app for a week and then opens checkout, we
//   want a clean slate, not stale credit.
//
// Riverpod scaffold so non-Widget code (checkout API call) reads it
// without prop-drilling through every screen.

import 'package:flutter_riverpod/flutter_riverpod.dart';

class AffiliateAttribution {
  const AffiliateAttribution.empty() : code = null;
  const AffiliateAttribution.withCode(String this.code);

  final String? code;
}

class AffiliateAttributionNotifier extends StateNotifier<AffiliateAttribution> {
  AffiliateAttributionNotifier() : super(AffiliateAttribution.empty());

  /// Captures a fresh affiliate code from the redirect URL. Last-touch
  /// wins: subsequent calls overwrite the previous attribution, so a
  /// viewer who hops between creators credits the most recent tap.
  void capture(String code) {
    if (code.trim().isEmpty) return;
    state = AffiliateAttribution.withCode(code);
  }

  /// Clear attribution after successful checkout so a follow-up
  /// purchase isn't credited to the same affiliate.
  void clear() {
    state = AffiliateAttribution.empty();
  }
}

final affiliateAttributionProvider =
    StateNotifierProvider<AffiliateAttributionNotifier, AffiliateAttribution>(
        (_) => AffiliateAttributionNotifier());
