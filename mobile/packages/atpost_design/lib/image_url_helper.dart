// Image URL helper — emits media-service URLs that respect the user's
// data-saver preference.
//
// media-service's `/v1/media/:id/serve` endpoint accepts `w=` (target
// width in pixels) and `q=` (quality 1..100) query params today; when
// data-saver is on we cap to roughly 0.5x normal width and lower the
// JPEG quality. The size enum keeps the call sites self-documenting.
//
// Call sites that don't yet care about data-saver can still call
// `resolveImageUrl(url, dataSaver: false)` and get the original URL
// back unchanged. The helper deliberately preserves any pre-existing
// query string so a URL that already carries `?token=...` is not
// clobbered.

enum ImageSize {
  /// Avatars, comment thumbnails, reaction strip avatars.
  small,

  /// Default for feed cards, posttube thumbnails, reel covers.
  medium,

  /// Hero / cover photos on profile and posttube watch screens.
  large,
}

class _ImageBudget {
  const _ImageBudget(this.width, this.quality);
  final int width;
  final int quality;
}

const Map<ImageSize, _ImageBudget> _normalBudgets = {
  ImageSize.small: _ImageBudget(96, 80),
  ImageSize.medium: _ImageBudget(480, 80),
  ImageSize.large: _ImageBudget(1080, 85),
};

const Map<ImageSize, _ImageBudget> _saverBudgets = {
  // ~0.5x normal width, q=60. Recon §F.2 calls out 0.5x explicitly.
  ImageSize.small: _ImageBudget(48, 60),
  ImageSize.medium: _ImageBudget(200, 60),
  ImageSize.large: _ImageBudget(540, 60),
};

/// Returns a URL suitable for `NetworkImage` / `Image.network`.
///
/// If [dataSaver] is true, appends `w=` and `q=` parameters that
/// roughly halve the bytes-on-wire. If the input is empty, an empty
/// string is returned (callers usually feed this through a
/// null-checking widget anyway).
String resolveImageUrl(
  String url, {
  required bool dataSaver,
  ImageSize size = ImageSize.medium,
}) {
  if (url.isEmpty) return url;

  // Data: URIs and asset bundles cannot be resized server-side.
  if (url.startsWith('data:') || url.startsWith('asset:')) {
    return url;
  }

  final budget = (dataSaver ? _saverBudgets : _normalBudgets)[size]!;

  final separator = url.contains('?') ? '&' : '?';
  return '$url${separator}w=${budget.width}&q=${budget.quality}';
}
