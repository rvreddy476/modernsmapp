import 'package:flutter/material.dart';

/// Top-of-screen feedback banner.
///
/// Replaces the default bottom SnackBar for anything the user needs to notice.
/// Three rules drove the design:
///
///  * It appears at the TOP. A message at the bottom of a tall phone screen,
///    below the keyboard, is routinely missed.
///  * Colour carries the meaning before the words do — green for "that worked",
///    red for "that did not". Someone who reads slowly, or not at all, still
///    learns the outcome.
///  * Errors linger longer than successes. A success is confirmation you
///    already expected; a failure is something you have to read and act on.
enum ToastKind { success, error, info }

class AppToast {
  const AppToast._();

  static void success(BuildContext context, String message) =>
      _show(context, message, ToastKind.success);

  static void error(BuildContext context, String message) =>
      _show(context, message, ToastKind.error);

  static void info(BuildContext context, String message) =>
      _show(context, message, ToastKind.info);

  static void _show(BuildContext context, String message, ToastKind kind) {
    final messenger = ScaffoldMessenger.maybeOf(context);
    if (messenger == null) return;

    // Drop any banner already on screen. Queued banners would otherwise stack
    // up and show a stale message after the user has moved on.
    messenger.removeCurrentMaterialBanner();

    final (bg, fg, icon) = switch (kind) {
      ToastKind.success => (
          const Color(0xFF1B7F4C),
          Colors.white,
          Icons.check_circle_outline,
        ),
      ToastKind.error => (
          const Color(0xFFB3261E),
          Colors.white,
          Icons.error_outline,
        ),
      ToastKind.info => (
          const Color(0xFF1F2933),
          Colors.white,
          Icons.info_outline,
        ),
    };

    messenger.showMaterialBanner(
      MaterialBanner(
        backgroundColor: bg,
        content: Row(
          children: [
            Icon(icon, color: fg, size: 22),
            const SizedBox(width: 12),
            Expanded(
              child: Text(
                message,
                style: TextStyle(
                  color: fg,
                  fontSize: 14.5,
                  height: 1.35,
                  fontWeight: FontWeight.w500,
                ),
              ),
            ),
          ],
        ),
        // A banner with no action stays until something dismisses it, so every
        // banner gets an explicit close and an auto-dismiss below.
        actions: [
          TextButton(
            onPressed: messenger.removeCurrentMaterialBanner,
            child: Text(
              'DISMISS',
              style: TextStyle(color: fg, fontWeight: FontWeight.w700),
            ),
          ),
        ],
      ),
    );

    final linger = kind == ToastKind.error
        ? const Duration(seconds: 6)
        : const Duration(seconds: 3);
    Future<void>.delayed(linger, messenger.removeCurrentMaterialBanner);
  }
}
