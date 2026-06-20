// Autoplay preference for video surfaces (Reels / PostTube).
//
// A single persisted bool, defaulting to ON, mirroring the persistence
// pattern of `data_saver_provider.dart`. Reels reads this in its autoplay
// gate so the "Autoplay" toggle in the video More sheet has a real effect.

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

const String kAutoplayStorageKey = 'autoplay_v1';

class AutoplayNotifier extends StateNotifier<bool> {
  AutoplayNotifier() : super(true) {
    _hydrate();
  }

  final FlutterSecureStorage _storage = const FlutterSecureStorage();

  Future<void> _hydrate() async {
    try {
      final raw = await _storage.read(key: kAutoplayStorageKey);
      if (raw != null) state = raw == 'true';
    } catch (_) {
      // Best-effort; keep default.
    }
  }

  Future<void> setEnabled(bool value) async {
    if (state == value) return;
    state = value;
    try {
      await _storage.write(key: kAutoplayStorageKey, value: '$value');
    } catch (_) {
      // Best-effort.
    }
  }

  Future<void> toggle() => setEnabled(!state);
}

/// `true` = autoplay videos; `false` = require a tap to play.
final autoplayProvider =
    StateNotifierProvider<AutoplayNotifier, bool>((ref) => AutoplayNotifier());
