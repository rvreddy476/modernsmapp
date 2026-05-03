// Commerce search — Sprint 2.
//
// AppBar with auto-focus search input. Empty input shows trending +
// recent searches. Results render as the same 2-col grid the catalog
// home uses. Filters icon → bottom sheet. Sort dropdown.
//
// Recent searches are kept in-memory only at v1 (no SharedPreferences
// dependency). Sprint 3 should persist them via the existing key-value
// service if one exists.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/commerce.dart';
import 'package:atpost_app/features/commerce/search_filters_sheet.dart';
import 'package:atpost_app/features/commerce/widgets/wishlist_button.dart';
import 'package:atpost_app/providers/commerce_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// In-memory recent search history. Capped at 8.
final _recentSearches = <String>[];

const _trendingSearches = <String>[
  'Cotton kurta',
  'Smartphone under 20k',
  'Running shoes',
  'Yoga mat',
  'Cookware set',
  'Wireless earbuds',
];

class SearchScreen extends ConsumerStatefulWidget {
  const SearchScreen({super.key, this.initialQuery});

  final String? initialQuery;

  @override
  ConsumerState<SearchScreen> createState() => _SearchScreenState();
}

class _SearchScreenState extends ConsumerState<SearchScreen> {
  late final TextEditingController _ctrl;
  late final FocusNode _focus;
  String _query = '';
  SearchFilters _filters = const SearchFilters();
  SearchSort _sort = SearchSort.relevance;

  @override
  void initState() {
    super.initState();
    _ctrl = TextEditingController(text: widget.initialQuery ?? '');
    _query = widget.initialQuery ?? '';
    _focus = FocusNode();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _focus.requestFocus();
    });
  }

  @override
  void dispose() {
    _ctrl.dispose();
    _focus.dispose();
    super.dispose();
  }

  void _commitQuery(String value) {
    final clean = value.trim();
    setState(() => _query = clean);
    if (clean.isNotEmpty) {
      _recentSearches.remove(clean);
      _recentSearches.insert(0, clean);
      while (_recentSearches.length > 8) {
        _recentSearches.removeLast();
      }
    }
  }

  Future<void> _openFilters() async {
    final result = await showSearchFiltersSheet(context, initial: _filters);
    if (result != null) {
      setState(() => _filters = result);
    }
  }

  @override
  Widget build(BuildContext context) {
    final hasQuery =
        _query.isNotEmpty || !_filters.isEmpty || _sort != SearchSort.relevance;
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: TextField(
          controller: _ctrl,
          focusNode: _focus,
          textInputAction: TextInputAction.search,
          onSubmitted: _commitQuery,
          style: AppTextStyles.body,
          decoration: InputDecoration(
            hintText: 'Search products',
            hintStyle:
                AppTextStyles.body.copyWith(color: AppColors.textMuted),
            border: InputBorder.none,
            suffixIcon: _ctrl.text.isEmpty
                ? null
                : IconButton(
                    icon: const Icon(Icons.clear, size: 18),
                    onPressed: () {
                      _ctrl.clear();
                      setState(() => _query = '');
                    },
                  ),
          ),
        ),
        actions: [
          Stack(
            children: [
              IconButton(
                tooltip: 'Filters',
                icon: const Icon(Icons.tune,
                    color: AppColors.textPrimary),
                onPressed: _openFilters,
              ),
              if (_filters.appliedCount > 0)
                Positioned(
                  top: 6,
                  right: 6,
                  child: Container(
                    padding: const EdgeInsets.symmetric(
                        horizontal: 5, vertical: 1),
                    decoration: BoxDecoration(
                      color: AppColors.postbookPrimary,
                      borderRadius: BorderRadius.circular(999),
                    ),
                    child: Text(
                      '${_filters.appliedCount}',
                      style: const TextStyle(
                        color: Colors.white,
                        fontSize: 10,
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                  ),
                ),
            ],
          ),
        ],
      ),
      body: Column(
        children: [
          if (hasQuery) _SortBar(
            sort: _sort,
            onSortChange: (s) => setState(() => _sort = s),
          ),
          Expanded(
            child: hasQuery
                ? _Results(
                    query: _query,
                    filters: _filters,
                    sort: _sort,
                  )
                : _SuggestionsBody(onTapTerm: (term) {
                    _ctrl.text = term;
                    _commitQuery(term);
                  }),
          ),
        ],
      ),
    );
  }
}

class _SortBar extends StatelessWidget {
  const _SortBar({required this.sort, required this.onSortChange});

  final SearchSort sort;
  final void Function(SearchSort) onSortChange;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(
          horizontal: AppSpacing.l, vertical: AppSpacing.s),
      decoration: const BoxDecoration(
        color: AppColors.bgPrimary,
        border: Border(bottom: BorderSide(color: AppColors.borderSubtle)),
      ),
      child: Row(
        children: [
          const Icon(Icons.swap_vert, size: 18, color: AppColors.textTertiary),
          const SizedBox(width: AppSpacing.s),
          Text('Sort:', style: AppTextStyles.bodySmall),
          const SizedBox(width: AppSpacing.s),
          DropdownButton<SearchSort>(
            value: sort,
            isDense: true,
            underline: const SizedBox.shrink(),
            dropdownColor: AppColors.bgSecondary,
            style: AppTextStyles.label,
            onChanged: (s) {
              if (s != null) onSortChange(s);
            },
            items: SearchSort.values
                .map((s) => DropdownMenuItem(
                      value: s,
                      child: Text(s.label),
                    ))
                .toList(),
          ),
        ],
      ),
    );
  }
}

class _SuggestionsBody extends StatelessWidget {
  const _SuggestionsBody({required this.onTapTerm});

  final void Function(String) onTapTerm;

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.all(AppSpacing.l),
      children: [
        if (_recentSearches.isNotEmpty) ...[
          Text('Recent searches', style: AppTextStyles.h3),
          const SizedBox(height: AppSpacing.s),
          Wrap(
            spacing: AppSpacing.m,
            runSpacing: AppSpacing.m,
            children:
                _recentSearches.map((t) => _TermChip(label: t, onTap: () => onTapTerm(t))).toList(),
          ),
          const SizedBox(height: AppSpacing.xxl),
        ],
        Text('Trending searches', style: AppTextStyles.h3),
        const SizedBox(height: AppSpacing.s),
        Wrap(
          spacing: AppSpacing.m,
          runSpacing: AppSpacing.m,
          children: _trendingSearches
              .map((t) => _TermChip(label: t, onTap: () => onTapTerm(t)))
              .toList(),
        ),
      ],
    );
  }
}

class _TermChip extends StatelessWidget {
  const _TermChip({required this.label, required this.onTap});

  final String label;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
      child: Container(
        padding: const EdgeInsets.symmetric(
            horizontal: AppSpacing.xxl, vertical: AppSpacing.m),
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.search,
                size: 14, color: AppColors.textTertiary),
            const SizedBox(width: AppSpacing.s),
            Text(label, style: AppTextStyles.bodySmall),
          ],
        ),
      ),
    );
  }
}

class _Results extends ConsumerWidget {
  const _Results({
    required this.query,
    required this.filters,
    required this.sort,
  });

  final String query;
  final SearchFilters filters;
  final SearchSort sort;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final searchQuery = SearchQuery(
      q: query.isEmpty ? null : query,
      filters: filters,
      sort: sort,
    );
    final pageAsync = ref.watch(productSearchProvider(searchQuery));
    return pageAsync.when(
      loading: () => const Center(
        child: CircularProgressIndicator(color: AppColors.postbookPrimary),
      ),
      error: (e, _) => Center(
        child: Padding(
          padding: const EdgeInsets.all(AppSpacing.xxl),
          child: Text(
            'Search failed.\n$e',
            style: AppTextStyles.body,
            textAlign: TextAlign.center,
          ),
        ),
      ),
      data: (page) {
        if (page.items.isEmpty) {
          return Center(
            child: Padding(
              padding: const EdgeInsets.all(AppSpacing.xxl),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  const Icon(Icons.search_off,
                      size: 56, color: AppColors.textGhost),
                  const SizedBox(height: AppSpacing.l),
                  Text(
                    'No products match your search.\nTry different keywords or clear some filters.',
                    style: AppTextStyles.body,
                    textAlign: TextAlign.center,
                  ),
                ],
              ),
            ),
          );
        }
        return GridView.builder(
          padding: const EdgeInsets.all(AppSpacing.l),
          gridDelegate:
              const SliverGridDelegateWithFixedCrossAxisCount(
            crossAxisCount: 2,
            mainAxisSpacing: AppSpacing.l,
            crossAxisSpacing: AppSpacing.l,
            childAspectRatio: 0.62,
          ),
          itemCount: page.items.length,
          itemBuilder: (ctx, i) =>
              _ResultCard(product: page.items[i]),
        );
      },
    );
  }
}

class _ResultCard extends StatelessWidget {
  const _ResultCard({required this.product});

  final Product product;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: () =>
          GoRouter.of(context).push('/commerce/product/${product.id}'),
      borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
      child: Container(
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Stack(
              children: [
                AspectRatio(
                  aspectRatio: 1,
                  child: ClipRRect(
                    borderRadius: const BorderRadius.vertical(
                      top: Radius.circular(AppSpacing.radiusLarge),
                    ),
                    child: product.primaryImageUrl == null
                        ? Container(color: AppColors.bgSecondary)
                        : Image.network(
                            product.primaryImageUrl!,
                            fit: BoxFit.cover,
                            errorBuilder: (_, _, _) => Container(
                              color: AppColors.bgSecondary,
                              child: const Icon(Icons.broken_image_outlined,
                                  color: AppColors.textGhost),
                            ),
                          ),
                  ),
                ),
                Positioned(
                  top: 4,
                  right: 4,
                  child: Container(
                    decoration: const BoxDecoration(
                      color: AppColors.bgPrimary,
                      shape: BoxShape.circle,
                    ),
                    child: WishlistButton(
                      productId: product.id,
                      snapshot: WishlistItemSnapshot(
                        title: product.title,
                        primaryImageUrl: product.primaryImageUrl,
                        sellingPrice: product.basePrice,
                        mrp: product.mrp,
                      ),
                      size: 18,
                      padded: false,
                    ),
                  ),
                ),
              ],
            ),
            Padding(
              padding: const EdgeInsets.all(AppSpacing.l),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text(
                    product.title,
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                    style: AppTextStyles.label.copyWith(
                      color: AppColors.textPrimary,
                    ),
                  ),
                  const SizedBox(height: AppSpacing.s),
                  Text(
                    'Rs. ${product.basePrice.toStringAsFixed(0)}',
                    style: AppTextStyles.h3,
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}
