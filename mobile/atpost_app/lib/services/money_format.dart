// Money formatting — Phase 2 wallet.
//
// Re-exports `formatRupees(int paise)` from `data/models/wallet.dart`
// so non-wallet surfaces (billpay, commerce, etc.) can import this
// thin module without dragging the entire wallet namespace. The
// canonical implementation stays in wallet.dart so display drift
// can't sneak in.
//
// Rules:
//   * paise (int) is the only unit we do arithmetic in.
//   * `₹1 = 100 paise`. Never multiply by 100.0; always integer math.
//   * Indian-style thousands grouping (12,34,567.89) — important for the
//     wallet hero card on the home screen.

import 'package:atpost_app/data/models/wallet.dart' as _wallet;

/// Format paise as rupees. Defers to the canonical implementation in
/// `data/models/wallet.dart` so the code path is identical everywhere.
String formatRupees(int paise, {bool withSymbol = true}) {
  return _wallet.formatRupees(paise, withSymbol: withSymbol);
}

/// Convenience: bucket an amount (paise) into the coarse band used for
/// telemetry. The privacy contract says "never log exact amounts" — call
/// sites should pass the bucket string, not the raw integer.
///
/// Buckets in rupees:
///   `0-99`, `100-499`, `500-999`, `1000-4999`, `5000-9999`,
///   `10000-49999`, `50000-99999`, `100000+`.
String amountBucket(int paise) {
  final r = paise ~/ 100;
  if (r < 100) return '0-99';
  if (r < 500) return '100-499';
  if (r < 1000) return '500-999';
  if (r < 5000) return '1000-4999';
  if (r < 10000) return '5000-9999';
  if (r < 50000) return '10000-49999';
  if (r < 100000) return '50000-99999';
  return '100000+';
}
