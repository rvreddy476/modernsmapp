// Shell telemetry — AtPost super-app shell.
//
// Mirrors the contract used by `pulse_telemetry.dart`: events are buffered in
// memory and flushed on a 30-second cadence to `POST /v1/analytics/events`.
// All props are enum-typed counters and stable identifiers — never user
// content, never raw search queries.
//
// Surface = the AtPost super-app shell: 5-tab bottom nav, the center create
// FAB, the unified search bar, and the Me-tab launcher grid. Each event
// answers a single product question:
//
//   - shellTabSelected         which tab does the user land on?
//   - shellSearchQueryRun      does search return results in each category?
//   - shellCreateOptionPicked  which create surface is hot vs cold?
//   - shellLauncherTileTapped  which of the 8 modules gets opened from Me?
//
// PRIVACY:
//   - Never log search query content. We log `category` + `has_results`.
//   - The shared `_bannedPropKeys` list rejects any caller that mistakenly
//     tries to attach `query`, `q`, `text`, message, etc.

import 'dart:async';
import 'dart:collection';

import 'package:atpost_app/core/utils/app_logger.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Keys we will never forward, even if a caller mistakenly includes them.
const Set<String> _bannedPropKeys = {
  'query',
  'q',
  'text',
  'search_text',
  'message',
  'message_body',
  'body',
  'photo',
  'photo_bytes',
  'image_data',
  'phone',
  'email',
  'lat',
  'lng',
  'latitude',
  'longitude',
  'location',
};

/// Stable event name catalog. Exported as constants so call sites stay
/// consistent and grep-able.
class ShellTelemetryEvents {
  ShellTelemetryEvents._();

  static const tabSelected = 'shell.tab.selected';
  static const searchQueryRun = 'shell.search.query.run';
  static const createOptionPicked = 'shell.create.option.picked';
  static const launcherTileTapped = 'shell.launcher.tile.tapped';
  static const inboxTabSelected = 'shell.inbox.tab.selected';
}

/// Tabs in the bottom nav. Used as the `tab` enum on `tabSelected`.
///
/// Active tabs (May 2026 redesign): home, wallet, create (FAB),
/// reels, explore. The legacy keys (search, inbox, me) remain
/// declared so downstream telemetry queries don't blow up if old
/// rows replay through the pipeline; new emit sites should use the
/// active set.
class ShellTab {
  ShellTab._();

  static const home = 'home';
  static const wallet = 'wallet';
  static const create = 'create';
  static const reels = 'reels';
  static const explore = 'explore';

  // Legacy / pre-May-2026 — retained for replay compat.
  static const search = 'search';
  static const inbox = 'inbox';
  static const me = 'me';
}

/// Create-sheet options. Used as the `option` enum on `createOptionPicked`.
class ShellCreateOption {
  ShellCreateOption._();

  static const post = 'post';
  static const reel = 'reel';
  static const story = 'story';
  static const question = 'question';
  static const live = 'live';
  static const listing = 'listing';
}

/// Modules in the Me-tab launcher grid. Used as the `module` enum on
/// `launcherTileTapped`.
class ShellModule {
  ShellModule._();

  static const feed = 'feed';
  static const posttube = 'posttube';
  static const reels = 'reels';
  static const pulse = 'pulse';
  static const qa = 'qa';
  static const shop = 'shop';
  static const wallet = 'wallet';
  static const billpay = 'billpay';
  static const mopedu = 'mopedu';
  static const more = 'more';
}

class _QueuedEvent {
  _QueuedEvent({
    required this.name,
    required this.timestamp,
    required this.props,
  });

  final String name;
  final DateTime timestamp;
  final Map<String, Object?> props;

  Map<String, dynamic> toJson() => {
        'name': name,
        'ts': timestamp.toUtc().toIso8601String(),
        'props': props,
      };
}

class ShellTelemetry {
  ShellTelemetry(this._client);

  final ApiClient _client;
  final Queue<_QueuedEvent> _buffer = Queue<_QueuedEvent>();
  Timer? _timer;
  bool _flushing = false;

  /// Largest buffer we hold in memory before dropping the oldest event.
  static const int _maxBuffered = 500;

  /// Flush cadence. Matches the pulse-telemetry default.
  static const Duration _flushInterval = Duration(seconds: 30);

  void start() {
    _timer ??= Timer.periodic(_flushInterval, (_) => _flush());
  }

  void stop() {
    _timer?.cancel();
    _timer = null;
  }

  /// Public entry point. Drops banned keys and enqueues.
  void emit(String name, {Map<String, Object?>? props}) {
    final cleaned = _sanitize(props ?? const {});
    if (_buffer.length >= _maxBuffered) {
      _buffer.removeFirst();
    }
    _buffer.add(
      _QueuedEvent(
        name: name,
        timestamp: DateTime.now(),
        props: cleaned,
      ),
    );
    start();
  }

  // ---- Convenience wrappers ----------------------------------------------

  /// `tab` is one of `ShellTab.*`.
  void shellTabSelected(String tab) {
    emit(ShellTelemetryEvents.tabSelected, props: {'tab': tab});
  }

  /// Tracks whether a search query in `category` returned anything. We never
  /// log query content — only the category and a boolean.
  void shellSearchQueryRun({
    required String category,
    required bool hasResults,
  }) {
    emit(
      ShellTelemetryEvents.searchQueryRun,
      props: {'category': category, 'has_results': hasResults},
    );
  }

  /// `option` is one of `ShellCreateOption.*`.
  void shellCreateOptionPicked(String option) {
    emit(
      ShellTelemetryEvents.createOptionPicked,
      props: {'option': option},
    );
  }

  /// `module` is one of `ShellModule.*`.
  void shellLauncherTileTapped(String module) {
    emit(
      ShellTelemetryEvents.launcherTileTapped,
      props: {'module': module},
    );
  }

  /// `tab` is one of `all | mentions | pulse | commerce | system`.
  void shellInboxTabSelected(String tab) {
    emit(
      ShellTelemetryEvents.inboxTabSelected,
      props: {'tab': tab},
    );
  }

  // ---- Internals ---------------------------------------------------------

  Map<String, Object?> _sanitize(Map<String, Object?> input) {
    final out = <String, Object?>{};
    input.forEach((key, value) {
      if (_bannedPropKeys.contains(key.toLowerCase())) {
        AppLogger.warn(
          'ShellTelemetry dropped banned key "$key"',
          tag: 'ShellTelemetry',
        );
        return;
      }
      if (value == null ||
          value is num ||
          value is bool ||
          value is String) {
        out[key] = value;
      } else if (value is List) {
        out[key] = value
            .where((e) => e == null || e is num || e is bool || e is String)
            .toList();
      } else {
        AppLogger.warn(
          'ShellTelemetry dropped unsupported type for key "$key"',
          tag: 'ShellTelemetry',
        );
      }
    });
    return out;
  }

  Future<void> _flush() async {
    if (_flushing || _buffer.isEmpty) return;
    _flushing = true;
    final batch = _buffer.toList(growable: false);
    try {
      await _client.post(
        '/v1/analytics/events',
        data: {
          'events': batch.map((e) => e.toJson()).toList(),
        },
      );
      for (var i = 0; i < batch.length && _buffer.isNotEmpty; i++) {
        _buffer.removeFirst();
      }
    } catch (e) {
      AppLogger.warn(
        'ShellTelemetry flush failed; will retry: $e',
        tag: 'ShellTelemetry',
      );
    } finally {
      _flushing = false;
    }
  }

  /// Test/QA hook — caller-driven flush.
  Future<void> flushNow() => _flush();
}

final shellTelemetryProvider = Provider<ShellTelemetry>((ref) {
  final client = ref.watch(apiClientProvider);
  final tel = ShellTelemetry(client);
  tel.start();
  ref.onDispose(tel.stop);
  return tel;
});
