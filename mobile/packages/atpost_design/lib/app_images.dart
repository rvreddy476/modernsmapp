import 'package:cached_network_image/cached_network_image.dart';
import 'package:flutter/material.dart';

// Image loading rules for every surface:
//
//  1. Never use raw Image.network / NetworkImage for app content — they
//     have no disk cache (cold scroll = full re-download) and decode at
//     the asset's native resolution (a 4000px photo decoded for a 350px
//     cell burns ~60MB and janks the raster thread).
//  2. Cap the decode size to what the cell actually renders. The caps
//     below are physical pixels: 1080 covers a full-width feed cell on
//     a 3x device; 256 covers any avatar.

/// Decode cap for full-width media (feed cards, product heroes).
const int kMediaDecodeWidth = 1080;

/// Decode cap for grid thumbnails (2–3 column layouts).
const int kThumbDecodeWidth = 540;

/// Decode cap for avatars.
const int kAvatarDecodeWidth = 256;

/// Drop-in [ImageProvider] replacement for `NetworkImage(url)` in
/// avatars (CircleAvatar.backgroundImage, DecorationImage). Cached on
/// disk and decoded at avatar size.
ImageProvider cachedAvatarProvider(String url) =>
    CachedNetworkImageProvider(url, maxWidth: kAvatarDecodeWidth);

/// Drop-in [ImageProvider] for larger imagery used through
/// DecorationImage. Defaults to the full-width media cap.
ImageProvider cachedImageProvider(String url, {int? decodeWidth}) =>
    CachedNetworkImageProvider(url, maxWidth: decodeWidth ?? kMediaDecodeWidth);

/// The standard network image widget: disk+memory cached, decode-capped,
/// with a quiet placeholder and error state that match the app surface.
class AppNetworkImage extends StatelessWidget {
  const AppNetworkImage(
    this.url, {
    super.key,
    this.fit = BoxFit.cover,
    this.width,
    this.height,
    this.decodeWidth = kMediaDecodeWidth,
    this.placeholderColor,
    this.error,
  });

  final String url;
  final BoxFit fit;
  final double? width;
  final double? height;

  /// Physical-pixel decode cap. Use [kThumbDecodeWidth] for grid cells
  /// and [kAvatarDecodeWidth] for tiny images.
  final int decodeWidth;

  /// Placeholder fill while loading (defaults to a neutral dark tone).
  final Color? placeholderColor;

  /// Widget shown when the image fails to load.
  final Widget? error;

  @override
  Widget build(BuildContext context) {
    return CachedNetworkImage(
      imageUrl: url,
      fit: fit,
      width: width,
      height: height,
      memCacheWidth: decodeWidth,
      fadeInDuration: const Duration(milliseconds: 120),
      placeholder: (context, _) => ColoredBox(
        color: placeholderColor ?? const Color(0xFF1A1A22),
      ),
      errorWidget: (context, _, _) =>
          error ??
          ColoredBox(
            color: placeholderColor ?? const Color(0xFF1A1A22),
            child: const Center(
              child: Icon(Icons.broken_image_outlined,
                  size: 20, color: Color(0xFF55555F)),
            ),
          ),
    );
  }
}
