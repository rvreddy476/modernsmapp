import 'package:atpost_app/core/utils/app_logger.dart';

/// Sprint 6 — Pulse crash-reporting breadcrumbs.
///
/// We do not yet bundle `sentry_flutter` (pubspec is frozen in this sprint).
/// This file is the no-op interface that screens call into. When the app
/// adopts Sentry / Crashlytics later, swap the body of `add()`, `error()`,
/// and `setUser()` to call into the real SDK; everything else stays.
///
/// Design constraints:
///   - Zero allocations in the hot path beyond the log call so we can
///     liberally instrument every screen.
///   - Body of breadcrumbs is *never* PII. Drop emails, phone numbers,
///     message bodies. The handoff (§6.7 of the spec) calls this out.
///   - Categories are namespaced: `pulse.<surface>.<event>` (e.g.
///     `pulse.orbital.drag_start`, `pulse.chat.send`). Don't invent ad-hoc
///     names — use the constants in [PulseBreadcrumbs] or add new ones.
class PulseBreadcrumbs {
  PulseBreadcrumbs._();

  /// Add an info breadcrumb. Forwards to AppLogger today; will forward to
  /// Sentry SDK when wired.
  static void add(
    String category, {
    String? message,
    Map<String, dynamic>? data,
  }) {
    final dataStr = (data == null || data.isEmpty) ? '' : ' $data';
    AppLogger.info(
      '[breadcrumb] $category${message != null ? ' — $message' : ''}$dataStr',
      tag: 'pulse',
    );
  }

  /// Note an exception/error breadcrumb. Mirrors `add()` but also threads
  /// the error + stacktrace through AppLogger.error which has special
  /// production handling.
  static void error(
    String message, {
    Object? error,
    StackTrace? stackTrace,
    Map<String, dynamic>? data,
  }) {
    final dataStr = (data == null || data.isEmpty) ? '' : ' $data';
    AppLogger.error(
      '[breadcrumb] $message$dataStr',
      tag: 'pulse',
      error: error,
      stackTrace: stackTrace,
    );
  }

  /// Sets the active user-id context for crash reports. Passing `null`
  /// clears it (used on logout). The user-id is the ONLY user identifier
  /// we attach — names, emails, phone numbers must never come through.
  static void setUser(String? userId) {
    AppLogger.info(
      '[breadcrumb] user.context userId=${userId ?? '(cleared)'}',
      tag: 'pulse',
    );
  }

  // ------------------------------------------------------------------
  // Helper hooks per Pulse surface. Screens import these by name so the
  // breadcrumb categories stay consistent and a future grep is sane.
  // ------------------------------------------------------------------

  /// Generic screen-open breadcrumb. Call from `initState`.
  static void screen(String name, {Map<String, dynamic>? data}) {
    add('pulse.screen.$name', data: data);
  }

  /// Orbital canvas — a high-frequency surface, so we only crumb the
  /// gesture lifecycle, never the per-frame draw.
  static void orbitalDragStart() => add('pulse.orbital.drag_start');
  static void orbitalDragRelease({required bool committed}) =>
      add('pulse.orbital.drag_release', data: {'committed': committed});

  /// Spark target picker — open/close pair so abandonment can be measured.
  static void sparkPickerOpen({String? candidateId}) => add(
        'pulse.spark_picker.open',
        data: {if (candidateId != null) 'candidate_id': candidateId},
      );
  static void sparkPickerClose({required bool sent}) =>
      add('pulse.spark_picker.close', data: {'sent': sent});

  /// Match inbox — open / select.
  static void matchInboxOpen() => add('pulse.matches.inbox_open');
  static void matchInboxSelect({required String matchId}) =>
      add('pulse.matches.select', data: {'match_id': matchId});

  /// Conversation — open/send/close. Body is NEVER attached.
  static void conversationOpen({required String conversationId}) => add(
        'pulse.conversation.open',
        data: {'conversation_id': conversationId},
      );
  static void conversationSend({required String conversationId}) => add(
        'pulse.conversation.send',
        data: {'conversation_id': conversationId},
      );
  static void conversationClose({required String conversationId}) => add(
        'pulse.conversation.close',
        data: {'conversation_id': conversationId},
      );

  /// Safety center — every section tap.
  static void safetyCenterOpen() => add('pulse.safety.center_open');
  static void safetySectionTap(String section) =>
      add('pulse.safety.section_tap', data: {'section': section});

  /// Premium — checkout lifecycle.
  static void premiumCheckoutStart({required String planId}) => add(
        'pulse.premium.checkout_start',
        data: {'plan_id': planId},
      );
  static void premiumCheckoutComplete({required String planId}) => add(
        'pulse.premium.checkout_complete',
        data: {'plan_id': planId},
      );
  static void premiumCheckoutFail({
    required String planId,
    String? reason,
  }) =>
      add(
        'pulse.premium.checkout_fail',
        data: {
          'plan_id': planId,
          if (reason != null) 'reason': reason,
        },
      );
}
