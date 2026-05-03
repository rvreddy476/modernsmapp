// Filters bottom-sheet for `SearchScreen`. Returns a `SearchFilters` value
// when the user taps Apply, or null on dismiss.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/commerce.dart';
import 'package:atpost_app/providers/commerce_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Shows the filters sheet and resolves with the chosen filters (or null
/// on dismiss).
Future<SearchFilters?> showSearchFiltersSheet(
  BuildContext context, {
  required SearchFilters initial,
}) {
  return showModalBottomSheet<SearchFilters>(
    context: context,
    isScrollControlled: true,
    backgroundColor: AppColors.bgPrimary,
    shape: const RoundedRectangleBorder(
      borderRadius:
          BorderRadius.vertical(top: Radius.circular(AppSpacing.radiusXL)),
    ),
    builder: (ctx) => _SearchFiltersSheet(initial: initial),
  );
}

class _SearchFiltersSheet extends ConsumerStatefulWidget {
  const _SearchFiltersSheet({required this.initial});

  final SearchFilters initial;

  @override
  ConsumerState<_SearchFiltersSheet> createState() =>
      _SearchFiltersSheetState();
}

class _SearchFiltersSheetState
    extends ConsumerState<_SearchFiltersSheet> {
  late SearchFilters _draft;
  RangeValues _priceRange = const RangeValues(0, 50000);

  @override
  void initState() {
    super.initState();
    _draft = widget.initial;
    _priceRange = RangeValues(
      widget.initial.priceMin ?? 0,
      widget.initial.priceMax ?? 50000,
    );
  }

  @override
  Widget build(BuildContext context) {
    final categoriesAsync = ref.watch(categoriesProvider);
    return DraggableScrollableSheet(
      initialChildSize: 0.85,
      minChildSize: 0.5,
      maxChildSize: 0.95,
      expand: false,
      builder: (ctx, scrollCtrl) {
        return Column(
          children: [
            const SizedBox(height: AppSpacing.s),
            Container(
              width: 36,
              height: 4,
              decoration: BoxDecoration(
                color: AppColors.borderMedium,
                borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
              ),
            ),
            const SizedBox(height: AppSpacing.l),
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: AppSpacing.l),
              child: Row(
                mainAxisAlignment: MainAxisAlignment.spaceBetween,
                children: [
                  Text('Filters', style: AppTextStyles.h2),
                  TextButton(
                    onPressed: () {
                      setState(() {
                        _draft = const SearchFilters();
                        _priceRange = const RangeValues(0, 50000);
                      });
                    },
                    child: const Text('Reset'),
                  ),
                ],
              ),
            ),
            Expanded(
              child: ListView(
                controller: scrollCtrl,
                padding: const EdgeInsets.all(AppSpacing.l),
                children: [
                  Text('Categories', style: AppTextStyles.h3),
                  const SizedBox(height: AppSpacing.s),
                  categoriesAsync.when(
                    loading: () => const SizedBox(
                      height: 32,
                      child: LinearProgressIndicator(
                        minHeight: 2,
                        color: AppColors.postbookPrimary,
                      ),
                    ),
                    error: (_, _) => Text(
                      'Could not load categories.',
                      style: AppTextStyles.bodySmall,
                    ),
                    data: (list) => Wrap(
                      spacing: AppSpacing.m,
                      runSpacing: AppSpacing.m,
                      children: list.where((c) => c.parentId == null).map((c) {
                        final selected = _draft.categoryIds.contains(c.id);
                        return _Chip(
                          label: c.name,
                          selected: selected,
                          onTap: () {
                            setState(() {
                              final next = List<String>.from(_draft.categoryIds);
                              if (selected) {
                                next.remove(c.id);
                              } else {
                                next.add(c.id);
                              }
                              _draft = _draft.copyWith(categoryIds: next);
                            });
                          },
                        );
                      }).toList(),
                    ),
                  ),
                  const SizedBox(height: AppSpacing.xxl),
                  Text('Price range', style: AppTextStyles.h3),
                  RangeSlider(
                    values: _priceRange,
                    min: 0,
                    max: 50000,
                    divisions: 50,
                    activeColor: AppColors.postbookPrimary,
                    labels: RangeLabels(
                      'Rs. ${_priceRange.start.round()}',
                      'Rs. ${_priceRange.end.round()}',
                    ),
                    onChanged: (v) => setState(() => _priceRange = v),
                  ),
                  Row(
                    mainAxisAlignment: MainAxisAlignment.spaceBetween,
                    children: [
                      Text('Rs. ${_priceRange.start.round()}',
                          style: AppTextStyles.label),
                      Text('Rs. ${_priceRange.end.round()}',
                          style: AppTextStyles.label),
                    ],
                  ),
                  const SizedBox(height: AppSpacing.xxl),
                  Text('Customer rating', style: AppTextStyles.h3),
                  const SizedBox(height: AppSpacing.s),
                  Wrap(
                    spacing: AppSpacing.m,
                    runSpacing: AppSpacing.m,
                    children: [4.0, 3.0, 2.0].map((r) {
                      final selected = _draft.ratingMin == r;
                      return _Chip(
                        label: '$r★ & up',
                        selected: selected,
                        onTap: () {
                          setState(() {
                            _draft = _draft.copyWith(
                                ratingMin: selected ? null : r);
                          });
                        },
                      );
                    }).toList(),
                  ),
                  const SizedBox(height: AppSpacing.xxl),
                  SwitchListTile(
                    contentPadding: EdgeInsets.zero,
                    activeColor: AppColors.postbookPrimary,
                    value: _draft.hasFreeShipping,
                    onChanged: (v) => setState(
                        () => _draft = _draft.copyWith(hasFreeShipping: v)),
                    title:
                        Text('Free shipping', style: AppTextStyles.body),
                  ),
                  SwitchListTile(
                    contentPadding: EdgeInsets.zero,
                    activeColor: AppColors.postbookPrimary,
                    value: _draft.hasCod,
                    onChanged: (v) =>
                        setState(() => _draft = _draft.copyWith(hasCod: v)),
                    title: Text('Cash on delivery',
                        style: AppTextStyles.body),
                  ),
                ],
              ),
            ),
            SafeArea(
              child: Padding(
                padding: const EdgeInsets.all(AppSpacing.l),
                child: SizedBox(
                  width: double.infinity,
                  child: ElevatedButton(
                    onPressed: () {
                      final out = _draft.copyWith(
                        priceMin:
                            _priceRange.start <= 0 ? null : _priceRange.start,
                        priceMax:
                            _priceRange.end >= 50000 ? null : _priceRange.end,
                      );
                      Navigator.of(context).pop(out);
                    },
                    style: ElevatedButton.styleFrom(
                      backgroundColor: AppColors.postbookPrimary,
                      padding: const EdgeInsets.symmetric(vertical: 14),
                    ),
                    child: const Text('Apply filters'),
                  ),
                ),
              ),
            ),
          ],
        );
      },
    );
  }
}

class _Chip extends StatelessWidget {
  const _Chip({
    required this.label,
    required this.selected,
    required this.onTap,
  });

  final String label;
  final bool selected;
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
          color: selected ? AppColors.postbookPrimary : AppColors.bgCard,
          borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
          border: Border.all(
            color: selected
                ? AppColors.postbookPrimary
                : AppColors.borderSubtle,
          ),
        ),
        child: Text(
          label,
          style: AppTextStyles.bodySmall.copyWith(
            color: selected ? Colors.white : AppColors.textSecondary,
            fontWeight: FontWeight.w600,
          ),
        ),
      ),
    );
  }
}
