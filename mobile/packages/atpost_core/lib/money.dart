// Money formatting — the canonical rupee formatter for every surface
// (wallet, billpay, commerce, mopedu). Pure integer math; no Flutter, no
// app types, so any package can depend on it.
//
// Rules:
//   * paise (int) is the only unit we do arithmetic in.
//   * `₹1 = 100 paise`. Never multiply by 100.0; always integer math.
//   * Indian-style thousands grouping (12,34,567.89).

/// Format paise as an Indian-grouped rupee string.
///
/// ```
/// formatRupees(0)         -> '₹0'
/// formatRupees(50)        -> '₹0.50'
/// formatRupees(123456789) -> '₹12,34,567.89'
/// ```
String formatRupees(int paise, {bool withSymbol = true}) {
  final negative = paise < 0;
  final abs = paise.abs();
  final rupees = abs ~/ 100;
  final fract = abs % 100;

  // Indian grouping: last 3 digits, then groups of 2.
  final rs = rupees.toString();
  final buf = StringBuffer();
  if (rs.length <= 3) {
    buf.write(rs);
  } else {
    final tail = rs.substring(rs.length - 3);
    var head = rs.substring(0, rs.length - 3);
    final groups = <String>[];
    while (head.length > 2) {
      groups.insert(0, head.substring(head.length - 2));
      head = head.substring(0, head.length - 2);
    }
    if (head.isNotEmpty) groups.insert(0, head);
    buf.write(groups.join(','));
    buf.write(',');
    buf.write(tail);
  }

  final sym = withSymbol ? '₹' : '';
  final sign = negative ? '-' : '';
  if (fract == 0) {
    return '$sign$sym${buf.toString()}';
  }
  final f = fract.toString().padLeft(2, '0');
  return '$sign$sym${buf.toString()}.$f';
}

/// Bucket an amount (paise) into the coarse band used for telemetry.
/// The privacy contract says "never log exact amounts" — call sites pass
/// the bucket string, not the raw integer.
///
/// Buckets in rupees: `0-99`, `100-499`, `500-999`, `1000-4999`,
/// `5000-9999`, `10000-49999`, `50000-99999`, `100000+`.
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
