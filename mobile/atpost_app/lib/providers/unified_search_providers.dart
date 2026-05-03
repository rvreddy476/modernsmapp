// Unified search providers — AtPost super-app shell.
//
// The shell's Search tab fans out a single query to seven upstream surfaces
// in parallel:
//
//   users        user-service          /v1/search/users (existing)
//   posts        post-service          searchExtras (existing)
//   reels        post-service          (stub — reels-search ships Sprint 2)
//   products     commerce-service      /v1/commerce/products?q=...
//   questions    qa-service            /v1/qa/search
//   billers      billpay-service       client-side filter on listProviders
//   restaurants  food-service          (stub — food module owned by another agent)
//
// Each section is capped at 5 for the `All` tab; each per-category tab is
// paginated through its own provider (the existing per-module search
// providers remain the source of truth — this file only owns the unified
// `All` aggregation and the recent / trending state).
//
// Recent searches are persisted in `flutter_secure_storage` under a single
// key (last 10, JSON-encoded). Trending searches are a static stub for v1.

import 'dart:convert';

import 'package:atpost_app/data/models/billpay.dart';
import 'package:atpost_app/data/models/commerce.dart';
import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/data/models/qa.dart';
import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/data/repositories/billpay_repository.dart';
import 'package:atpost_app/data/repositories/commerce_repository.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:atpost_app/providers/qa_provider.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

const int kUnifiedSearchSectionCap = 5;

/// Categories surfaced in the search tab strip.
enum SearchCategory {
  all,
  users,
  posts,
  reels,
  products,
  questions,
  billers,
  restaurants,
}

extension SearchCategoryX on SearchCategory {
  String get key {
    switch (this) {
      case SearchCategory.all:
        return 'all';
      case SearchCategory.users:
        return 'users';
      case SearchCategory.posts:
        return 'posts';
      case SearchCategory.reels:
        return 'reels';
      case SearchCategory.products:
        return 'products';
      case SearchCategory.questions:
        return 'questions';
      case SearchCategory.billers:
        return 'billers';
      case SearchCategory.restaurants:
        return 'restaurants';
    }
  }

  String get label {
    switch (this) {
      case SearchCategory.all:
        return 'All';
      case SearchCategory.users:
        return 'Users';
      case SearchCategory.posts:
        return 'Posts';
      case SearchCategory.reels:
        return 'Reels';
      case SearchCategory.products:
        return 'Products';
      case SearchCategory.questions:
        return 'Questions';
      case SearchCategory.billers:
        return 'Billers';
      case SearchCategory.restaurants:
        return 'Restaurants';
    }
  }
}

/// Aggregate result for the `All` tab. Each section is capped at
/// `kUnifiedSearchSectionCap` so the screen stays scannable.
class UnifiedSearchResults {
  const UnifiedSearchResults({
    this.users = const <User>[],
    this.posts = const <Post>[],
    this.reels = const <Post>[],
    this.products = const <Product>[],
    this.questions = const <Question>[],
    this.billers = const <BillProvider>[],
    this.restaurants = const <UnifiedRestaurant>[],
  });

  final List<User> users;
  final List<Post> posts;
  final List<Post> reels;
  final List<Product> products;
  final List<Question> questions;
  final List<BillProvider> billers;
  final List<UnifiedRestaurant> restaurants;

  bool get isEmpty =>
      users.isEmpty &&
      posts.isEmpty &&
      reels.isEmpty &&
      products.isEmpty &&
      questions.isEmpty &&
      billers.isEmpty &&
      restaurants.isEmpty;
}

/// Thin restaurant placeholder. The food module is owned by a sibling agent;
/// when that lands we'll replace this with the real model and stop returning
/// an empty list.
class UnifiedRestaurant {
  const UnifiedRestaurant({
    required this.id,
    required this.name,
    this.cuisine,
  });

  final String id;
  final String name;
  final String? cuisine;
}

/// Unified `All`-tab provider. Trims the query, returns an empty result set
/// for empty input (callers render trending instead), and fans out in
/// parallel — one slow upstream doesn't block the others because each
/// fetcher swallows its own errors and falls back to an empty list.
final unifiedSearchProvider = FutureProvider.autoDispose
    .family<UnifiedSearchResults, String>((ref, rawQuery) async {
  final query = rawQuery.trim();
  if (query.isEmpty) return const UnifiedSearchResults();

  final results = await Future.wait<dynamic>([
    _searchUsers(ref, query),
    _searchPosts(ref, query),
    _searchReels(ref, query),
    _searchProducts(ref, query),
    _searchQuestions(ref, query),
    _searchBillers(ref, query),
    _searchRestaurants(ref, query),
  ]);

  return UnifiedSearchResults(
    users: (results[0] as List<User>).take(kUnifiedSearchSectionCap).toList(),
    posts: (results[1] as List<Post>).take(kUnifiedSearchSectionCap).toList(),
    reels: (results[2] as List<Post>).take(kUnifiedSearchSectionCap).toList(),
    products:
        (results[3] as List<Product>).take(kUnifiedSearchSectionCap).toList(),
    questions:
        (results[4] as List<Question>).take(kUnifiedSearchSectionCap).toList(),
    billers: (results[5] as List<BillProvider>)
        .take(kUnifiedSearchSectionCap)
        .toList(),
    restaurants: (results[6] as List<UnifiedRestaurant>)
        .take(kUnifiedSearchSectionCap)
        .toList(),
  );
});

/// Per-category provider — used by each individual category tab to drive its
/// own (eventually paginated) list. v1 reuses the same fetchers without
/// pagination; Sprint 2 swaps each entry for the existing module-specific
/// search provider with a cursor.
final unifiedSearchByCategoryProvider = FutureProvider.autoDispose
    .family<List<Object>, _SearchKey>((ref, key) async {
  final query = key.query.trim();
  if (query.isEmpty) return const <Object>[];
  switch (key.category) {
    case SearchCategory.all:
      // The All tab is served by `unifiedSearchProvider`; this branch is
      // only reachable if a caller passes `all` here by mistake.
      return const <Object>[];
    case SearchCategory.users:
      return _searchUsers(ref, query);
    case SearchCategory.posts:
      return _searchPosts(ref, query);
    case SearchCategory.reels:
      return _searchReels(ref, query);
    case SearchCategory.products:
      return _searchProducts(ref, query);
    case SearchCategory.questions:
      return _searchQuestions(ref, query);
    case SearchCategory.billers:
      return _searchBillers(ref, query);
    case SearchCategory.restaurants:
      return _searchRestaurants(ref, query);
  }
});

class _SearchKey {
  const _SearchKey(this.query, this.category);

  final String query;
  final SearchCategory category;

  @override
  bool operator ==(Object other) =>
      other is _SearchKey &&
      other.query == query &&
      other.category == category;

  @override
  int get hashCode => Object.hash(query, category);
}

/// Public alias so screens can build the family key without seeing the
/// private class name.
_SearchKey searchKey(String query, SearchCategory category) =>
    _SearchKey(query, category);

// ─── Per-source fetchers ───────────────────────────────────────────────

Future<List<User>> _searchUsers(Ref ref, String query) async {
  try {
    final repo = ref.watch(userRepositoryProvider);
    final page = await repo.searchUsers(query);
    return page.users;
  } catch (_) {
    return const <User>[];
  }
}

/// Posts-search isn't a backend endpoint yet (the post-search service is
/// still being implemented). Stub the surface so the UI builds; the call
/// site renders an empty section. Sprint 2 wires this to
/// `postRepository.searchPosts(query)` once the endpoint ships.
Future<List<Post>> _searchPosts(Ref ref, String query) async {
  return const <Post>[];
}

/// Reels-search isn't a backend endpoint yet (Sprint 2 work). Stub the
/// surface so the UI builds; the call site shows an empty section.
Future<List<Post>> _searchReels(Ref ref, String query) async {
  return const <Post>[];
}

Future<List<Product>> _searchProducts(Ref ref, String query) async {
  try {
    final repo = ref.watch(commerceRepositoryProvider);
    final page = await repo.listProducts(q: query);
    return page.items;
  } catch (_) {
    return const <Product>[];
  }
}

Future<List<Question>> _searchQuestions(Ref ref, String query) async {
  try {
    return await ref.watch(
      qaSearchProvider(QaSearchParams(query: query)).future,
    );
  } catch (_) {
    return const <Question>[];
  }
}

/// Billers don't have a backend search endpoint; we filter client-side over
/// `listProviders()`. Acceptable for v1 because the provider catalog is
/// small (~hundreds, not millions).
Future<List<BillProvider>> _searchBillers(Ref ref, String query) async {
  try {
    final repo = ref.watch(billpayRepositoryProvider);
    final providers = await repo.getProviders();
    final lower = query.toLowerCase();
    return providers
        .where(
          (p) =>
              p.name.toLowerCase().contains(lower) ||
              p.shortName.toLowerCase().contains(lower),
        )
        .toList(growable: false);
  } catch (_) {
    return const <BillProvider>[];
  }
}

/// Food module is owned by a sibling agent. Until it lands the unified
/// search shows an empty restaurant section so the UI structure stays
/// stable.
Future<List<UnifiedRestaurant>> _searchRestaurants(
  Ref ref,
  String query,
) async {
  return const <UnifiedRestaurant>[];
}

// ─── Recent + trending ──────────────────────────────────────────────────

const String _kRecentSearchesKey = 'shell_recent_searches_v1';
const int _kRecentSearchesMax = 10;

class RecentSearchesNotifier extends StateNotifier<List<String>> {
  RecentSearchesNotifier() : super(const <String>[]) {
    _hydrate();
  }

  final FlutterSecureStorage _storage = const FlutterSecureStorage();

  Future<void> _hydrate() async {
    try {
      final raw = await _storage.read(key: _kRecentSearchesKey);
      if (raw == null || raw.isEmpty) return;
      final list = (jsonDecode(raw) as List)
          .whereType<String>()
          .toList(growable: false);
      state = list;
    } catch (_) {
      // Corrupt blob — wipe and start clean.
      await _storage.delete(key: _kRecentSearchesKey);
    }
  }

  Future<void> add(String query) async {
    final q = query.trim();
    if (q.isEmpty) return;
    final next = <String>[q];
    for (final existing in state) {
      if (existing == q) continue;
      next.add(existing);
      if (next.length >= _kRecentSearchesMax) break;
    }
    state = next;
    await _persist();
  }

  Future<void> remove(String query) async {
    state = state.where((q) => q != query).toList(growable: false);
    await _persist();
  }

  Future<void> clear() async {
    state = const <String>[];
    await _storage.delete(key: _kRecentSearchesKey);
  }

  Future<void> _persist() async {
    try {
      await _storage.write(
        key: _kRecentSearchesKey,
        value: jsonEncode(state),
      );
    } catch (_) {
      // Ignore persistence failures; in-memory state is still valid for the
      // session.
    }
  }
}

final recentSearchesProvider =
    StateNotifierProvider<RecentSearchesNotifier, List<String>>(
  (ref) => RecentSearchesNotifier(),
);

/// Static trending list for v1. Replace with a real
/// `/v1/search/trending` call once the discovery service ships it.
final trendingSearchesProvider = Provider<List<String>>((ref) {
  return const <String>[
    'wedding photographers',
    'iphone 16',
    'electricity bill',
    'reels',
    'pulse near me',
  ];
});
