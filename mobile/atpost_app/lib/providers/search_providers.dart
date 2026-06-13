// Riverpod providers for the new multi-entity ranked search.
//
// `multiEntitySearchProvider` runs the initial fetch (all six entity
// buckets in one request). For per-bucket "Load more", the screen
// reads the current bucket's `next_cursor` and calls
// `MultiEntitySearchController.loadMore(entity)` on a
// StateNotifierProvider that owns the merged-results state.
//
// Click analytics live behind a tiny one-shot provider so screens can
// `ref.read(searchRecordClickProvider).call(...)` without pulling the
// repository directly.

import 'package:atpost_app/data/models/search_results.dart';
import 'package:atpost_app/data/repositories/search_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Initial fetch for a query. Empty / whitespace queries return an
/// empty result so the UI can render its "type something" hint
/// without round-tripping the network.
final multiEntitySearchProvider = FutureProvider.autoDispose
    .family<MultiEntitySearchResults, String>((ref, rawQuery) async {
  final q = rawQuery.trim();
  if (q.isEmpty) return const MultiEntitySearchResults();
  final repo = ref.watch(searchRepositoryProvider);
  return repo.multiEntitySearch(query: q);
});

/// Controller that owns merged-results state for a given query and
/// drives per-bucket pagination. Screens watch this for the live
/// state and call `loadMore(entity)` on tap of a "Show more" button.
///
/// One controller instance per (query) — keyed by the
/// StateNotifierProvider.family so query changes get a fresh slate.
class MultiEntitySearchController
    extends StateNotifier<AsyncValue<MultiEntitySearchResults>> {
  MultiEntitySearchController(this._repo, this._query)
      : super(const AsyncValue.loading()) {
    _bootstrap();
  }

  final SearchRepository _repo;
  final String _query;

  // Tracks per-entity "loading more" so the screen can show a spinner
  // on the right "Show more" button without re-rendering the whole tab.
  final Set<SearchEntity> _loadingMore = {};

  bool isLoadingMore(SearchEntity entity) => _loadingMore.contains(entity);

  Future<void> _bootstrap() async {
    final q = _query.trim();
    if (q.isEmpty) {
      state = const AsyncValue.data(MultiEntitySearchResults());
      return;
    }
    try {
      final data = await _repo.multiEntitySearch(query: q);
      state = AsyncValue.data(data);
    } catch (e, st) {
      state = AsyncValue.error(e, st);
    }
  }

  Future<void> refresh() async {
    state = const AsyncValue.loading();
    await _bootstrap();
  }

  /// Fetch the next page for one entity bucket and merge it into the
  /// current state. No-op when the bucket has no cursor.
  Future<void> loadMore(SearchEntity entity) async {
    final current = state.value;
    if (current == null) return;
    final cursor = _cursorFor(current, entity);
    if (cursor == null || cursor.isEmpty) return;
    if (_loadingMore.contains(entity)) return;
    _loadingMore.add(entity);
    // Bump state to itself to trigger listeners that read isLoadingMore.
    state = AsyncValue.data(current);
    try {
      final page = await _repo.multiEntitySearch(
        query: _query.trim(),
        types: [entity],
        cursors: {entity: cursor},
      );
      state = AsyncValue.data(_mergeBucket(current, entity, page));
    } catch (_) {
      // Keep existing state on pagination error; the caller can retry.
    } finally {
      _loadingMore.remove(entity);
      final v = state.value;
      if (v != null) state = AsyncValue.data(v);
    }
  }

  String? _cursorFor(MultiEntitySearchResults r, SearchEntity entity) {
    switch (entity) {
      case SearchEntity.posts:
        return r.posts.nextCursor;
      case SearchEntity.users:
        return r.users.nextCursor;
      case SearchEntity.hashtags:
        return r.hashtags.nextCursor;
      case SearchEntity.products:
        return r.products.nextCursor;
      case SearchEntity.communities:
        return r.communities.nextCursor;
      case SearchEntity.channels:
        return r.channels.nextCursor;
    }
  }

  MultiEntitySearchResults _mergeBucket(
    MultiEntitySearchResults current,
    SearchEntity entity,
    MultiEntitySearchResults page,
  ) {
    switch (entity) {
      case SearchEntity.posts:
        return current.copyWith(posts: current.posts.mergeWith(page.posts));
      case SearchEntity.users:
        return current.copyWith(users: current.users.mergeWith(page.users));
      case SearchEntity.hashtags:
        return current.copyWith(
            hashtags: current.hashtags.mergeWith(page.hashtags));
      case SearchEntity.products:
        return current.copyWith(
            products: current.products.mergeWith(page.products));
      case SearchEntity.communities:
        return current.copyWith(
            communities: current.communities.mergeWith(page.communities));
      case SearchEntity.channels:
        return current.copyWith(
            channels: current.channels.mergeWith(page.channels));
    }
  }
}

/// Per-query controller. `.family<String>` keys it so navigating to a
/// new search rebuilds from scratch (and the autoDispose tears the old
/// one down).
final multiEntitySearchControllerProvider = StateNotifierProvider.autoDispose
    .family<MultiEntitySearchController,
        AsyncValue<MultiEntitySearchResults>, String>((ref, query) {
  return MultiEntitySearchController(
    ref.watch(searchRepositoryProvider),
    query,
  );
});

/// Convenience: best-effort click logger. Returns a callable so screens
/// don't have to thread the repository through.
class SearchClickLogger {
  SearchClickLogger(this._repo);

  final SearchRepository _repo;

  Future<void> call({
    required String queryId,
    required SearchEntity entityType,
    required String entityId,
    required int position,
  }) =>
      _repo.recordClick(
        queryId: queryId,
        entityType: entityType,
        entityId: entityId,
        position: position,
      );
}

final searchClickLoggerProvider = Provider<SearchClickLogger>((ref) {
  return SearchClickLogger(ref.watch(searchRepositoryProvider));
});

/// Multi-entity autocomplete suggestions for an input prefix.
final autocompleteProvider = FutureProvider.autoDispose
    .family<List<AutocompleteItem>, String>((ref, prefix) async {
  final q = prefix.trim();
  if (q.isEmpty) return const <AutocompleteItem>[];
  final repo = ref.watch(searchRepositoryProvider);
  return repo.autocomplete(query: q);
});
