// Search tab — AtPost super-app shell.
//
// One search bar, eight category tabs (`All / Users / Posts / Reels /
// Products / Questions / Billers / Restaurants`). The `All` tab shows the
// top results from each category in sectioned form; per-category tabs
// re-use the same fetchers without the section cap.
//
// Empty-query state shows recent searches (persisted in
// `flutter_secure_storage` via `recentSearchesProvider`) and trending
// searches (static stub).

import 'dart:async';

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/billpay.dart';
import 'package:atpost_app/data/models/commerce.dart';
import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/data/models/qa.dart';
import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/providers/unified_search_providers.dart';
import 'package:atpost_app/services/shell_telemetry.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class SearchTab extends ConsumerStatefulWidget {
  const SearchTab({super.key});

  @override
  ConsumerState<SearchTab> createState() => _SearchTabState();
}

class _SearchTabState extends ConsumerState<SearchTab>
    with TickerProviderStateMixin {
  static const _kDebounce = Duration(milliseconds: 300);

  late final TabController _tabController;
  final TextEditingController _controller = TextEditingController();
  final FocusNode _focus = FocusNode();
  Timer? _debounce;
  String _query = '';

  static const _categories = SearchCategory.values;

  @override
  void initState() {
    super.initState();
    _tabController = TabController(
      length: _categories.length,
      vsync: this,
    );
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) _focus.requestFocus();
    });
  }

  @override
  void dispose() {
    _tabController.dispose();
    _controller.dispose();
    _focus.dispose();
    _debounce?.cancel();
    super.dispose();
  }

  void _onChanged(String raw) {
    _debounce?.cancel();
    _debounce = Timer(_kDebounce, () {
      if (!mounted) return;
      setState(() => _query = raw.trim());
    });
  }

  void _onSubmit(String raw) {
    final q = raw.trim();
    if (q.isEmpty) return;
    setState(() => _query = q);
    ref.read(recentSearchesProvider.notifier).add(q);
    _emitTelemetry(q);
  }

  void _emitTelemetry(String q) {
    // We never log query content. Just signal that a category was queried
    // and whether the user got non-empty results.
    final tel = ref.read(shellTelemetryProvider);
    final cat = _categories[_tabController.index];
    // Best-effort: read whatever's currently in the cache for this query +
    // category. Listeners will re-emit on tab change.
    final asyncResults = cat == SearchCategory.all
        ? ref.read(unifiedSearchProvider(q))
        : ref.read(
            unifiedSearchByCategoryProvider(searchKey(q, cat)),
          );
    final hasResults = asyncResults.maybeWhen(
      data: (v) {
        if (v is UnifiedSearchResults) return !v.isEmpty;
        if (v is List) return v.isNotEmpty;
        return false;
      },
      orElse: () => false,
    );
    tel.shellSearchQueryRun(category: cat.key, hasResults: hasResults);
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: SafeArea(
        child: Column(
          children: [
            _buildSearchBar(),
            _buildTabBar(),
            Expanded(
              child: _query.isEmpty
                  ? _buildEmptyState()
                  : TabBarView(
                      controller: _tabController,
                      children: [
                        for (final cat in _categories) _buildResultsView(cat),
                      ],
                    ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildSearchBar() {
    return Padding(
      padding: const EdgeInsets.fromLTRB(12, 8, 12, 8),
      child: Row(
        children: [
          Expanded(
            child: Container(
              height: 44,
              padding: const EdgeInsets.symmetric(horizontal: 14),
              decoration: BoxDecoration(
                color: AppColors.bgSecondary,
                borderRadius: BorderRadius.circular(99),
                border: Border.all(color: AppColors.borderSubtle),
              ),
              child: Row(
                children: [
                  const Icon(
                    Icons.search,
                    color: AppColors.textTertiary,
                    size: 18,
                  ),
                  const SizedBox(width: 8),
                  Expanded(
                    child: TextField(
                      controller: _controller,
                      focusNode: _focus,
                      style: AppTextStyles.body,
                      decoration: const InputDecoration(
                        border: InputBorder.none,
                        isCollapsed: true,
                        hintText: 'Search AtPost',
                        hintStyle: TextStyle(color: AppColors.textDim),
                      ),
                      textInputAction: TextInputAction.search,
                      onChanged: _onChanged,
                      onSubmitted: _onSubmit,
                    ),
                  ),
                  if (_query.isNotEmpty || _controller.text.isNotEmpty)
                    GestureDetector(
                      onTap: () {
                        _controller.clear();
                        setState(() => _query = '');
                      },
                      child: const Icon(
                        Icons.close,
                        color: AppColors.textTertiary,
                        size: 18,
                      ),
                    ),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildTabBar() {
    return TabBar(
      controller: _tabController,
      isScrollable: true,
      labelColor: AppColors.postbookPrimary,
      unselectedLabelColor: AppColors.textTertiary,
      indicatorColor: AppColors.postbookPrimary,
      labelStyle: AppTextStyles.label,
      tabs: [
        for (final cat in _categories) Tab(text: cat.label),
      ],
    );
  }

  Widget _buildEmptyState() {
    final recent = ref.watch(recentSearchesProvider);
    final trending = ref.watch(trendingSearchesProvider);
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        if (recent.isNotEmpty) ...[
          Row(
            children: [
              Text('Recent', style: AppTextStyles.h3),
              const Spacer(),
              TextButton(
                onPressed: () =>
                    ref.read(recentSearchesProvider.notifier).clear(),
                child: Text(
                  'Clear',
                  style: AppTextStyles.label.copyWith(
                    color: AppColors.textTertiary,
                  ),
                ),
              ),
            ],
          ),
          const SizedBox(height: 8),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              for (final q in recent)
                _Chip(
                  label: q,
                  icon: Icons.history,
                  onTap: () {
                    _controller.text = q;
                    setState(() => _query = q);
                  },
                ),
            ],
          ),
          const SizedBox(height: 24),
        ],
        Text('Trending', style: AppTextStyles.h3),
        const SizedBox(height: 8),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            for (final q in trending)
              _Chip(
                label: q,
                icon: Icons.trending_up,
                onTap: () {
                  _controller.text = q;
                  setState(() => _query = q);
                  ref.read(recentSearchesProvider.notifier).add(q);
                },
              ),
          ],
        ),
      ],
    );
  }

  Widget _buildResultsView(SearchCategory cat) {
    if (cat == SearchCategory.all) {
      final asyncAll = ref.watch(unifiedSearchProvider(_query));
      return asyncAll.when(
        data: _renderAll,
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => _ErrorView(message: '$e'),
      );
    }
    final asyncList = ref.watch(
      unifiedSearchByCategoryProvider(searchKey(_query, cat)),
    );
    return asyncList.when(
      data: (list) => _renderCategory(cat, list),
      loading: () => const Center(child: CircularProgressIndicator()),
      error: (e, _) => _ErrorView(message: '$e'),
    );
  }

  Widget _renderAll(UnifiedSearchResults r) {
    if (r.isEmpty) return const _NoResults();
    return ListView(
      padding: const EdgeInsets.all(12),
      children: [
        if (r.users.isNotEmpty) _Section(title: 'People', items: [
          for (final u in r.users) _UserTile(user: u),
        ]),
        if (r.posts.isNotEmpty)
          _Section(title: 'Posts', items: [
            for (final p in r.posts) _PostTile(post: p),
          ]),
        if (r.reels.isNotEmpty)
          _Section(title: 'Reels', items: [
            for (final p in r.reels) _PostTile(post: p),
          ]),
        if (r.products.isNotEmpty)
          _Section(title: 'Products', items: [
            for (final p in r.products) _ProductTile(product: p),
          ]),
        if (r.questions.isNotEmpty)
          _Section(title: 'Questions', items: [
            for (final q in r.questions) _QuestionTile(question: q),
          ]),
        if (r.billers.isNotEmpty)
          _Section(title: 'Billers', items: [
            for (final b in r.billers) _BillerTile(provider: b),
          ]),
        if (r.restaurants.isNotEmpty)
          _Section(title: 'Restaurants', items: [
            for (final res in r.restaurants)
              _RestaurantTile(restaurant: res),
          ]),
      ],
    );
  }

  Widget _renderCategory(SearchCategory cat, List<Object> list) {
    if (list.isEmpty) return const _NoResults();
    return ListView.separated(
      padding: const EdgeInsets.all(12),
      itemCount: list.length,
      separatorBuilder: (_, _) => const SizedBox(height: 8),
      itemBuilder: (context, i) {
        final item = list[i];
        switch (cat) {
          case SearchCategory.users:
            return _UserTile(user: item as User);
          case SearchCategory.posts:
          case SearchCategory.reels:
            return _PostTile(post: item as Post);
          case SearchCategory.products:
            return _ProductTile(product: item as Product);
          case SearchCategory.questions:
            return _QuestionTile(question: item as Question);
          case SearchCategory.billers:
            return _BillerTile(provider: item as BillProvider);
          case SearchCategory.restaurants:
            return _RestaurantTile(restaurant: item as UnifiedRestaurant);
          case SearchCategory.all:
            return const SizedBox.shrink();
        }
      },
    );
  }
}

// ─── Section + tiles ────────────────────────────────────────────────────

class _Section extends StatelessWidget {
  const _Section({required this.title, required this.items});

  final String title;
  final List<Widget> items;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(4, 12, 4, 8),
          child: Text(title, style: AppTextStyles.h3),
        ),
        ...items,
        const SizedBox(height: 4),
      ],
    );
  }
}

class _UserTile extends StatelessWidget {
  const _UserTile({required this.user});

  final User user;

  @override
  Widget build(BuildContext context) {
    return ListTile(
      contentPadding: EdgeInsets.zero,
      leading: CircleAvatar(
        radius: 20,
        backgroundColor: AppColors.bgTertiary,
        child: Text(
          (user.displayName.isNotEmpty
                  ? user.displayName[0]
                  : '?')
              .toUpperCase(),
          style: AppTextStyles.label,
        ),
      ),
      title: Text(user.displayName, style: AppTextStyles.label),
      subtitle: Text('@${user.username}', style: AppTextStyles.bodySmall),
      onTap: () => context.push('/profile/${user.id}'),
    );
  }
}

class _PostTile extends StatelessWidget {
  const _PostTile({required this.post});

  final Post post;

  @override
  Widget build(BuildContext context) {
    return ListTile(
      contentPadding: EdgeInsets.zero,
      leading: Container(
        width: 44,
        height: 44,
        decoration: BoxDecoration(
          color: AppColors.bgTertiary,
          borderRadius: BorderRadius.circular(8),
        ),
        child: const Icon(Icons.article, color: AppColors.textDim),
      ),
      title: Text(
        'Post by ${post.authorName ?? 'someone'}',
        style: AppTextStyles.label,
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      subtitle: Text(
        post.content,
        style: AppTextStyles.bodySmall,
        maxLines: 2,
        overflow: TextOverflow.ellipsis,
      ),
      onTap: () => context.push('/comments/${post.id}'),
    );
  }
}

class _ProductTile extends StatelessWidget {
  const _ProductTile({required this.product});

  final Product product;

  @override
  Widget build(BuildContext context) {
    return ListTile(
      contentPadding: EdgeInsets.zero,
      leading: Container(
        width: 44,
        height: 44,
        decoration: BoxDecoration(
          color: AppColors.bgTertiary,
          borderRadius: BorderRadius.circular(8),
        ),
        child: const Icon(
          Icons.shopping_bag,
          color: AppColors.statusWarning,
        ),
      ),
      title: Text(
        product.title,
        style: AppTextStyles.label,
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      subtitle: Text(
        '${product.currency} ${product.basePrice.toStringAsFixed(0)}',
        style: AppTextStyles.bodySmall,
      ),
      onTap: () => context.push('/commerce/product/${product.id}'),
    );
  }
}

class _QuestionTile extends StatelessWidget {
  const _QuestionTile({required this.question});

  final Question question;

  @override
  Widget build(BuildContext context) {
    return ListTile(
      contentPadding: EdgeInsets.zero,
      leading: const CircleAvatar(
        radius: 20,
        backgroundColor: AppColors.bgTertiary,
        child: Icon(
          Icons.help_outline,
          color: AppColors.posttubePrimary,
        ),
      ),
      title: Text(
        question.title,
        style: AppTextStyles.label,
        maxLines: 2,
        overflow: TextOverflow.ellipsis,
      ),
      subtitle: Text(
        '${question.answerCount} answers',
        style: AppTextStyles.bodySmall,
      ),
      onTap: () => context.push('/qa/question/${question.id}'),
    );
  }
}

class _BillerTile extends StatelessWidget {
  const _BillerTile({required this.provider});

  final BillProvider provider;

  @override
  Widget build(BuildContext context) {
    return ListTile(
      contentPadding: EdgeInsets.zero,
      leading: const CircleAvatar(
        radius: 20,
        backgroundColor: AppColors.bgTertiary,
        child: Icon(
          Icons.receipt_long,
          color: AppColors.statusSuccess,
        ),
      ),
      title: Text(provider.name, style: AppTextStyles.label),
      subtitle: Text(provider.shortName, style: AppTextStyles.bodySmall),
      onTap: () => context.push(
        '/billpay/add-account?providerId=${provider.id}',
      ),
    );
  }
}

class _RestaurantTile extends StatelessWidget {
  const _RestaurantTile({required this.restaurant});

  final UnifiedRestaurant restaurant;

  @override
  Widget build(BuildContext context) {
    return ListTile(
      contentPadding: EdgeInsets.zero,
      leading: const CircleAvatar(
        radius: 20,
        backgroundColor: AppColors.bgTertiary,
        child: Icon(Icons.restaurant, color: AppColors.statusWarning),
      ),
      title: Text(restaurant.name, style: AppTextStyles.label),
      subtitle: Text(
        restaurant.cuisine ?? 'Restaurant',
        style: AppTextStyles.bodySmall,
      ),
      onTap: () {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Food coming soon')),
        );
      },
    );
  }
}

class _Chip extends StatelessWidget {
  const _Chip({
    required this.label,
    required this.icon,
    required this.onTap,
  });

  final String label;
  final IconData icon;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Material(
      color: Colors.transparent,
      child: InkWell(
        borderRadius: BorderRadius.circular(99),
        onTap: onTap,
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
          decoration: BoxDecoration(
            color: AppColors.bgSecondary,
            borderRadius: BorderRadius.circular(99),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(icon, size: 14, color: AppColors.textTertiary),
              const SizedBox(width: 6),
              Text(label, style: AppTextStyles.bodySmall),
            ],
          ),
        ),
      ),
    );
  }
}

class _NoResults extends StatelessWidget {
  const _NoResults();

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(36),
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            const Icon(Icons.search_off, color: AppColors.textDim, size: 48),
            const SizedBox(height: 12),
            Text('No results', style: AppTextStyles.h2),
            const SizedBox(height: 4),
            Text(
              'Try a different word.',
              style: AppTextStyles.bodySmall,
              textAlign: TextAlign.center,
            ),
          ],
        ),
      ),
    );
  }
}

class _ErrorView extends StatelessWidget {
  const _ErrorView({required this.message});

  final String message;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Text(
          'Search failed:\n$message',
          textAlign: TextAlign.center,
          style: AppTextStyles.bodySmall,
        ),
      ),
    );
  }
}
