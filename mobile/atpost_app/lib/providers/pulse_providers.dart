import 'package:atpost_app/data/models/pulse.dart';
import 'package:atpost_app/data/repositories/pulse_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

/// Sprint 2 — Pulse providers backing the new orbital surface.
///
/// `pulseTodayProvider` is auto-disposed and refetches whenever the surface
/// remounts; that's intentional — the home pulse should feel "fresh" each
/// time the tab is opened. Offline cache is delegated to the in-memory
/// `_PulseMemoryCache` below; if/when Hive is wired into the app's DI,
/// swap the cache layer here without touching the screens.

/// Today's curated pulse — `GET /v1/dating/pulse/today`.
final pulseTodayProvider = FutureProvider.autoDispose<PulsePage>((ref) async {
  final repo = ref.watch(pulseRepositoryProvider);
  try {
    final page = await repo.getPulseToday();
    _PulseMemoryCache.instance.savePulseToday(page);
    return page;
  } catch (e) {
    final cached = _PulseMemoryCache.instance.readPulseToday();
    if (cached != null) return cached;
    rethrow;
  }
});

/// Nebula pool — `GET /v1/dating/pulse/nebula?filter=...`.
///
/// `family` param is the filter string (e.g. "passed", "queued").
final pulseNebulaProvider = FutureProvider.autoDispose
    .family<PulsePage, String>((ref, filter) async {
      final repo = ref.watch(pulseRepositoryProvider);
      try {
        final page = await repo.getPulseNebula(filter: filter);
        _PulseMemoryCache.instance.saveNebula(filter, page);
        return page;
      } catch (e) {
        final cached = _PulseMemoryCache.instance.readNebula(filter);
        if (cached != null) return cached;
        rethrow;
      }
    });

/// Display mode for the Pulse hero surface.
enum PulseMode { orbital, list }

/// Storage key for the persisted mode (the spec calls for SharedPreferences
/// but it's not in pubspec — using `flutter_secure_storage`, which is already
/// the codebase's standard small-KV store).
const _kPulseModeStorageKey = 'pulse_mode_v1';

/// Persisted Pulse mode. Reading is synchronous (StateProvider) but the value
/// is hydrated by `pulseModeBootstrapProvider` on first read.
final pulseModeProvider = StateProvider<PulseMode>((ref) => PulseMode.orbital);

/// Internal: hydrates the mode from secure storage and pushes it into the
/// state provider. Also writes back any change.
final pulseModeHydratorProvider = Provider<_PulseModeHydrator>((ref) {
  final hydrator = _PulseModeHydrator(ref);
  hydrator.start();
  ref.listen<PulseMode>(pulseModeProvider, (prev, next) {
    if (prev == next) return;
    hydrator.persist(next);
  });
  return hydrator;
});

class _PulseModeHydrator {
  _PulseModeHydrator(this._ref);

  final Ref _ref;
  final FlutterSecureStorage _storage = const FlutterSecureStorage();
  bool _hydrated = false;

  Future<void> start() async {
    if (_hydrated) return;
    try {
      final raw = await _storage.read(key: _kPulseModeStorageKey);
      if (raw == 'list') {
        _ref.read(pulseModeProvider.notifier).state = PulseMode.list;
      } else if (raw == 'orbital') {
        _ref.read(pulseModeProvider.notifier).state = PulseMode.orbital;
      }
    } catch (_) {
      // Ignore — fall back to default.
    }
    _hydrated = true;
  }

  Future<void> persist(PulseMode mode) async {
    try {
      await _storage.write(
        key: _kPulseModeStorageKey,
        value: mode == PulseMode.list ? 'list' : 'orbital',
      );
    } catch (_) {
      // Best-effort; don't crash on storage failure.
    }
  }
}

// ---------------------------------------------------------------------------
// Sprint 3 — Stash, Sparks, Matches.
// ---------------------------------------------------------------------------

/// The viewer's stash shelf. Refetched whenever a stash mutation happens
/// (add / remove) — callers should `ref.invalidate(stashProvider)` after.
final stashProvider = FutureProvider.autoDispose<List<PulseCard>>((ref) async {
  final repo = ref.watch(pulseRepositoryProvider);
  return repo.getStash();
});

/// Incoming sparks (people who Sparked the viewer but no mutual yet).
/// Used by the "Sparks waiting" tab on the match inbox.
final incomingSparksProvider =
    FutureProvider.autoDispose<List<IncomingSpark>>((ref) async {
  final repo = ref.watch(pulseRepositoryProvider);
  return repo.getIncomingSparks();
});

/// Match inbox feed — `status` is one of `all`, `active`, `quiet`,
/// `sparks-waiting`. Refetched on tab change because the family key changes.
final pulseMatchesProvider = FutureProvider.autoDispose
    .family<List<MatchSummary>, String>((ref, status) async {
  final repo = ref.watch(pulseRepositoryProvider);
  return repo.getPulseMatches(status: status);
});

/// Single-match detail — drives the chat banner / extend gate.
final pulseMatchProvider = FutureProvider.autoDispose
    .family<MatchDetail?, String>((ref, id) async {
  final repo = ref.watch(pulseRepositoryProvider);
  return repo.getMatch(id);
});

/// Resolve a match by its conversation id. The chat surface only knows the
/// conversation id (its route param), so we walk the match list once to find
/// the corresponding match, then hydrate the full detail. If nothing
/// matches, returns null and the chat screen renders without a banner.
final pulseMatchByConversationProvider = FutureProvider.autoDispose
    .family<MatchDetail?, String>((ref, conversationId) async {
  final repo = ref.watch(pulseRepositoryProvider);
  final matches = await repo.getPulseMatches();
  for (final m in matches) {
    if (m.conversationId == conversationId) {
      return repo.getMatch(m.id);
    }
  }
  return null;
});

// ---------------------------------------------------------------------------
// Sprint 5 — Premium tier + data export.
// ---------------------------------------------------------------------------

/// Live list of plans from `GET /v1/dating/premium/plans`. Auto-disposed so
/// the cards refresh when the premium screen reopens.
final premiumPlansProvider =
    FutureProvider.autoDispose<List<PremiumPlan>>((ref) async {
  final repo = ref.watch(pulseRepositoryProvider);
  return repo.getPremiumPlans();
});

/// The viewer's current premium state. Used by paywalls (decide whether to
/// upsell or fast-path), and by the premium screen header (show "Manage" vs
/// "Subscribe"). Invalidate after checkout completes / cancel.
final premiumStateProvider =
    FutureProvider.autoDispose<PremiumState>((ref) async {
  final repo = ref.watch(pulseRepositoryProvider);
  return repo.getPremium();
});

/// History of DPDP data exports. The export screen lists past requests +
/// surfaces a CTA for a fresh request (rate-limited 1/7d server-side).
final dataExportsProvider =
    FutureProvider.autoDispose<List<DataExportRecord>>((ref) async {
  final repo = ref.watch(pulseRepositoryProvider);
  return repo.getDataExports();
});

/// Tiny in-memory cache so an offline relaunch shows the last-known pulse
/// instead of a spinner. Singleton — replace with a Hive box in S6 polish.
class _PulseMemoryCache {
  _PulseMemoryCache._();
  static final _PulseMemoryCache instance = _PulseMemoryCache._();

  PulsePage? _today;
  final Map<String, PulsePage> _nebula = {};

  void savePulseToday(PulsePage page) => _today = page;
  PulsePage? readPulseToday() => _today;

  void saveNebula(String filter, PulsePage page) => _nebula[filter] = page;
  PulsePage? readNebula(String filter) => _nebula[filter];
}
