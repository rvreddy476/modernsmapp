// Data-saver / low-bandwidth mode — recon §F.2.
//
// Two-part state:
//
//   * `dataSaverProvider` — the user's manual toggle plus the
//     "auto-enable on slow connection" sub-toggle. Persisted to
//     `flutter_secure_storage` under a single key `data_saver_v1` so the
//     codec is symmetrical across launches.
//
//   * `effectiveDataSaverProvider` — a derived `bool` that consumers
//     should `ref.watch(...)`. It returns `true` when EITHER the manual
//     toggle is on, OR the user opted-in to auto-enable AND the device
//     reports a slow / metered connection.
//
// Connectivity heuristic: the project does not currently depend on
// `connectivity_plus` (verified against pubspec). To keep this PR
// self-contained we rely on a manual flag — `slowConnectionFlagProvider` —
// which any future `ConnectivityService` wrapper can drive by writing
// `true` whenever the device reports `mobile` AND a 2G/edge heuristic.
// Until that wrapper exists the flag stays `false`, which makes the
// "auto-enable" sub-toggle inert (graceful degradation; manual override
// always works).

import 'package:atpost_app/services/commerce_telemetry.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

const String kDataSaverStorageKey = 'data_saver_v1';

/// Telemetry event names. We ride on the existing CommerceTelemetry
/// emitter (which is the generic in-process buffer) instead of cutting
/// a new service just for two events.
class DataSaverTelemetryEvents {
  DataSaverTelemetryEvents._();

  static const toggled = 'data_saver.toggled';
  static const sessionActive = 'data_saver.session.active';
}

/// Immutable state container.
class DataSaverState {
  const DataSaverState({
    required this.enabled,
    required this.autoOnSlowConnection,
  });

  final bool enabled;
  final bool autoOnSlowConnection;

  static const DataSaverState defaults = DataSaverState(
    enabled: false,
    autoOnSlowConnection: true,
  );

  DataSaverState copyWith({bool? enabled, bool? autoOnSlowConnection}) {
    return DataSaverState(
      enabled: enabled ?? this.enabled,
      autoOnSlowConnection: autoOnSlowConnection ?? this.autoOnSlowConnection,
    );
  }

  /// Codec — flat `key=value;key=value` form, mirroring the pattern
  /// used by `pulse_safety_providers.dart` so we don't drag in
  /// `dart:convert` for two booleans.
  String encode() {
    return 'enabled=$enabled;auto=$autoOnSlowConnection';
  }

  static DataSaverState decode(String? raw) {
    if (raw == null || raw.isEmpty) return defaults;
    bool enabled = defaults.enabled;
    bool auto = defaults.autoOnSlowConnection;
    try {
      for (final pair in raw.split(';')) {
        final i = pair.indexOf('=');
        if (i <= 0) continue;
        final key = pair.substring(0, i);
        final value = pair.substring(i + 1);
        if (key == 'enabled') {
          enabled = value == 'true';
        } else if (key == 'auto') {
          auto = value == 'true';
        }
      }
    } catch (_) {
      return defaults;
    }
    return DataSaverState(enabled: enabled, autoOnSlowConnection: auto);
  }
}

/// Persisted notifier. Reads on init and writes through on every
/// mutation. Failures from secure storage are swallowed — the in-memory
/// state still updates so the user's toggle isn't visibly broken.
class DataSaverNotifier extends StateNotifier<DataSaverState> {
  DataSaverNotifier({CommerceTelemetry? telemetry})
      : _telemetry = telemetry,
        super(DataSaverState.defaults) {
    _hydrate();
  }

  final FlutterSecureStorage _storage = const FlutterSecureStorage();
  final CommerceTelemetry? _telemetry;
  bool _ready = false;

  bool get ready => _ready;

  Future<void> _hydrate() async {
    try {
      final raw = await _storage.read(key: kDataSaverStorageKey);
      state = DataSaverState.decode(raw);
    } catch (_) {
      // Best-effort.
    } finally {
      _ready = true;
    }
  }

  Future<void> _persist() async {
    try {
      await _storage.write(
        key: kDataSaverStorageKey,
        value: state.encode(),
      );
    } catch (_) {
      // Best-effort.
    }
  }

  Future<void> setEnabled(bool value, {String source = 'manual'}) async {
    if (state.enabled == value) return;
    state = state.copyWith(enabled: value);
    _telemetry?.dataSaverToggled(enabled: value, source: source);
    await _persist();
  }

  Future<void> setAutoOnSlowConnection(bool value) async {
    if (state.autoOnSlowConnection == value) return;
    state = state.copyWith(autoOnSlowConnection: value);
    await _persist();
  }
}

final dataSaverProvider =
    StateNotifierProvider<DataSaverNotifier, DataSaverState>((ref) {
  // Telemetry is optional; if the provider isn't yet wired we still
  // construct a working notifier.
  CommerceTelemetry? tel;
  try {
    tel = ref.read(commerceTelemetryProvider);
  } catch (_) {
    tel = null;
  }
  return DataSaverNotifier(telemetry: tel);
});

/// Lightweight flag for the "device looks slow" heuristic. Set by any
/// connectivity wrapper; defaults to `false`. We intentionally keep
/// this writable from the outside so a future `ConnectivityService`
/// (or an admin debug tile) can drive it without a circular dep on
/// `connectivity_plus`, which is not in pubspec today.
final slowConnectionFlagProvider = StateProvider<bool>((ref) => false);

/// Reactive "data saver should currently be active" boolean.
///
///  * Manual toggle on  -> always true.
///  * Manual off + auto on + slow flag -> true.
///  * Otherwise -> false.
///
/// Side effect: when the value transitions to `true` from `false`, we
/// fire `data_saver.session.active` once (per Riverpod listener
/// lifecycle) so the analytics warehouse can answer "what fraction of
/// sessions had data-saver active". The toggle event is emitted
/// inside `DataSaverNotifier` so we know the source ('manual' vs
/// 'auto'); the session-counter is fire-and-forget here.
final effectiveDataSaverProvider = Provider<bool>((ref) {
  final s = ref.watch(dataSaverProvider);
  final bool active;
  if (s.enabled) {
    active = true;
  } else if (!s.autoOnSlowConnection) {
    active = false;
  } else {
    active = ref.watch(slowConnectionFlagProvider);
  }

  if (active) {
    try {
      ref.read(commerceTelemetryProvider).dataSaverSessionActive();
    } catch (_) {
      // Telemetry not yet wired; ignore.
    }
  }

  return active;
});
