import 'package:atpost_app/data/models/pulse.dart';
import 'package:atpost_app/data/repositories/pulse_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

/// Sprint 4 — Riverpod providers for the safety center, verification ladder,
/// vouching, trusted contact, and live-location share.
///
/// Storage keys for the local trusted contact + quiet hours.
/// Pubspec already ships `flutter_secure_storage` so we standardise on it.
const _kTrustedContactStorageKey = 'pulse_trusted_contact_v1';
const _kQuietHoursStorageKey = 'pulse_quiet_hours_v1';
const _kSafetyModeStorageKey = 'pulse_safety_mode_active_v1';

/// Vouches FOR me (people who have vouched / been asked to vouch for me).
final vouchesForMeProvider =
    FutureProvider.autoDispose<List<Vouch>>((ref) async {
  final repo = ref.watch(pulseRepositoryProvider);
  return repo.getVouchesForMe();
});

/// Vouches I sent (asked others to vouch for me).
final vouchesSentProvider =
    FutureProvider.autoDispose<List<Vouch>>((ref) async {
  final repo = ref.watch(pulseRepositoryProvider);
  return repo.getVouchesSent();
});

/// A single trusted contact saved on the device. We persist locally so the
/// safety center always has something to ping even before the server-side
/// profile patch lands. Backend mirroring happens via `updateProfile`.
class TrustedContact {
  final String id;
  final String name;
  final String phone;
  final String? relationship;

  const TrustedContact({
    required this.id,
    required this.name,
    required this.phone,
    this.relationship,
  });

  Map<String, String> toMap() => {
        'id': id,
        'name': name,
        'phone': phone,
        if (relationship != null) 'relationship': relationship!,
      };

  factory TrustedContact.fromMap(Map<String, dynamic> raw) {
    return TrustedContact(
      id: (raw['id'] ?? '').toString(),
      name: (raw['name'] ?? '').toString(),
      phone: (raw['phone'] ?? '').toString(),
      relationship: raw['relationship']?.toString(),
    );
  }
}

/// Notifier for the device-local trusted contact. Reads on first use,
/// writes through to flutter_secure_storage on every mutation.
class TrustedContactNotifier extends StateNotifier<TrustedContact?> {
  TrustedContactNotifier() : super(null) {
    _hydrate();
  }

  final FlutterSecureStorage _storage = const FlutterSecureStorage();
  bool _ready = false;

  bool get ready => _ready;

  Future<void> _hydrate() async {
    try {
      final raw = await _storage.read(key: _kTrustedContactStorageKey);
      if (raw == null || raw.isEmpty) {
        _ready = true;
        return;
      }
      // Naive `key=value;key=value` codec keeps us off `dart:convert`.
      final map = <String, dynamic>{};
      for (final pair in raw.split(';')) {
        final i = pair.indexOf('=');
        if (i <= 0) continue;
        map[pair.substring(0, i)] = pair.substring(i + 1);
      }
      state = TrustedContact.fromMap(map);
    } catch (_) {
      // Best-effort.
    } finally {
      _ready = true;
    }
  }

  Future<void> save(TrustedContact contact) async {
    state = contact;
    try {
      final encoded = contact.toMap().entries
          .map((e) => '${e.key}=${e.value}')
          .join(';');
      await _storage.write(
        key: _kTrustedContactStorageKey,
        value: encoded,
      );
    } catch (_) {
      // Best-effort.
    }
  }

  Future<void> clear() async {
    state = null;
    try {
      await _storage.delete(key: _kTrustedContactStorageKey);
    } catch (_) {
      // Best-effort.
    }
  }
}

final trustedContactProvider =
    StateNotifierProvider<TrustedContactNotifier, TrustedContact?>((ref) {
  return TrustedContactNotifier();
});

/// Quiet hours window. Default 22:00 -> 08:00 per spec.
class QuietHours {
  final int startHour;
  final int startMinute;
  final int endHour;
  final int endMinute;
  final bool enabled;

  const QuietHours({
    required this.startHour,
    required this.startMinute,
    required this.endHour,
    required this.endMinute,
    this.enabled = true,
  });

  static const QuietHours defaults = QuietHours(
    startHour: 22,
    startMinute: 0,
    endHour: 8,
    endMinute: 0,
  );

  String get encoded =>
      '$enabled|$startHour:$startMinute|$endHour:$endMinute';

  static QuietHours decode(String? raw) {
    if (raw == null || raw.isEmpty) return defaults;
    try {
      final parts = raw.split('|');
      if (parts.length < 3) return defaults;
      final enabled = parts[0] == 'true';
      final start = parts[1].split(':');
      final end = parts[2].split(':');
      return QuietHours(
        startHour: int.tryParse(start[0]) ?? defaults.startHour,
        startMinute:
            int.tryParse(start.length > 1 ? start[1] : '') ??
                defaults.startMinute,
        endHour: int.tryParse(end[0]) ?? defaults.endHour,
        endMinute:
            int.tryParse(end.length > 1 ? end[1] : '') ?? defaults.endMinute,
        enabled: enabled,
      );
    } catch (_) {
      return defaults;
    }
  }

  QuietHours copyWith({
    int? startHour,
    int? startMinute,
    int? endHour,
    int? endMinute,
    bool? enabled,
  }) {
    return QuietHours(
      startHour: startHour ?? this.startHour,
      startMinute: startMinute ?? this.startMinute,
      endHour: endHour ?? this.endHour,
      endMinute: endMinute ?? this.endMinute,
      enabled: enabled ?? this.enabled,
    );
  }
}

class QuietHoursNotifier extends StateNotifier<QuietHours> {
  QuietHoursNotifier() : super(QuietHours.defaults) {
    _hydrate();
  }

  final FlutterSecureStorage _storage = const FlutterSecureStorage();

  Future<void> _hydrate() async {
    try {
      final raw = await _storage.read(key: _kQuietHoursStorageKey);
      state = QuietHours.decode(raw);
    } catch (_) {
      // Best-effort.
    }
  }

  Future<void> save(QuietHours next) async {
    state = next;
    try {
      await _storage.write(key: _kQuietHoursStorageKey, value: next.encoded);
    } catch (_) {
      // Best-effort.
    }
  }
}

final quietHoursProvider =
    StateNotifierProvider<QuietHoursNotifier, QuietHours>((ref) {
  return QuietHoursNotifier();
});

/// Safety mode: while active, conversations show a "Safety mode active"
/// watermark. Driven by the panic flow + share-location toggle.
class SafetyModeState {
  final bool active;
  final DateTime? until;
  final String? trigger; // 'panic' | 'share_location' | null

  const SafetyModeState({
    required this.active,
    this.until,
    this.trigger,
  });

  static const SafetyModeState idle = SafetyModeState(active: false);

  Duration? get remaining {
    final u = until;
    if (u == null) return null;
    final diff = u.difference(DateTime.now());
    return diff.isNegative ? Duration.zero : diff;
  }
}

class SafetyModeNotifier extends StateNotifier<SafetyModeState> {
  SafetyModeNotifier() : super(SafetyModeState.idle) {
    _hydrate();
  }

  final FlutterSecureStorage _storage = const FlutterSecureStorage();

  Future<void> _hydrate() async {
    try {
      final raw = await _storage.read(key: _kSafetyModeStorageKey);
      if (raw == null || raw.isEmpty) return;
      final parts = raw.split('|');
      if (parts.length < 3) return;
      final until = DateTime.tryParse(parts[1]);
      if (until == null || until.isBefore(DateTime.now())) {
        await _storage.delete(key: _kSafetyModeStorageKey);
        return;
      }
      state = SafetyModeState(
        active: parts[0] == 'true',
        until: until,
        trigger: parts[2].isEmpty ? null : parts[2],
      );
    } catch (_) {
      // Best-effort.
    }
  }

  Future<void> activate({
    required Duration duration,
    String trigger = 'panic',
  }) async {
    final until = DateTime.now().add(duration);
    state = SafetyModeState(active: true, until: until, trigger: trigger);
    try {
      await _storage.write(
        key: _kSafetyModeStorageKey,
        value: 'true|${until.toIso8601String()}|$trigger',
      );
    } catch (_) {
      // Best-effort.
    }
  }

  Future<void> stop() async {
    state = SafetyModeState.idle;
    try {
      await _storage.delete(key: _kSafetyModeStorageKey);
    } catch (_) {
      // Best-effort.
    }
  }
}

final safetyModeProvider =
    StateNotifierProvider<SafetyModeNotifier, SafetyModeState>((ref) {
  return SafetyModeNotifier();
});
