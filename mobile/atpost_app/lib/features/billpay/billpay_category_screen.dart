// Bill-pay category screen — Phase 2.
//
// Lists providers for a given category. Search box filters by name; for
// state-aware categories (electricity/gas/water) a state filter chip strip
// at the top narrows the list further.
//
// On tap: if the user already has a saved account for that provider, jump
// straight to the account detail screen. Otherwise route to the add-account
// flow with `providerId` prefilled.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/billpay.dart';
import 'package:atpost_app/providers/billpay_providers.dart';
import 'package:atpost_app/services/billpay_telemetry.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class BillPayCategoryScreen extends ConsumerStatefulWidget {
  const BillPayCategoryScreen({super.key, required this.categoryId});

  final String categoryId;

  @override
  ConsumerState<BillPayCategoryScreen> createState() =>
      _BillPayCategoryScreenState();
}

class _BillPayCategoryScreenState
    extends ConsumerState<BillPayCategoryScreen> {
  String _query = '';
  String? _stateFilter;
  bool _firedOpened = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_firedOpened) return;
      _firedOpened = true;
      ref
          .read(billpayTelemetryProvider)
          .billpayCategoryOpened(widget.categoryId);
    });
  }

  @override
  Widget build(BuildContext context) {
    final categories = ref.watch(billCategoriesProvider);
    final providersAsync = ref.watch(
      billProvidersProvider(
        ProvidersQuery(categoryId: widget.categoryId, state: _stateFilter),
      ),
    );
    final accounts = ref.watch(billAccountsProvider);

    final categoryName = categories.maybeWhen(
      data: (list) => list
          .firstWhere(
            (c) => c.id == widget.categoryId,
            orElse: () => const BillCategory(
              id: '',
              name: 'Providers',
              icon: '',
              sortOrder: 0,
              isActive: true,
            ),
          )
          .name,
      orElse: () => 'Providers',
    );

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text(categoryName, style: AppTextStyles.h2),
        leading: IconButton(
          icon: const Icon(
            Icons.arrow_back_ios_new_rounded,
            color: AppColors.textPrimary,
            size: 18,
          ),
          onPressed: () => context.pop(),
        ),
      ),
      body: Column(
        children: [
          Padding(
            padding: const EdgeInsets.all(AppSpacing.l),
            child: TextField(
              onChanged: (v) => setState(() => _query = v.trim().toLowerCase()),
              style: AppTextStyles.body.copyWith(
                color: AppColors.textPrimary,
              ),
              decoration: InputDecoration(
                hintText: 'Search providers',
                hintStyle: AppTextStyles.bodySmall,
                prefixIcon: const Icon(
                  Icons.search_rounded,
                  color: AppColors.textTertiary,
                ),
                filled: true,
                fillColor: AppColors.bgTertiary,
                border: OutlineInputBorder(
                  borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
                  borderSide: BorderSide.none,
                ),
                contentPadding: const EdgeInsets.symmetric(
                  horizontal: AppSpacing.l,
                  vertical: AppSpacing.l,
                ),
              ),
            ),
          ),
          providersAsync.maybeWhen(
            data: (list) {
              final allStates = <String>{};
              for (final p in list) {
                allStates.addAll(p.states);
              }
              if (allStates.isEmpty) return const SizedBox.shrink();
              final sorted = allStates.toList()..sort();
              return SizedBox(
                height: 36,
                child: ListView(
                  scrollDirection: Axis.horizontal,
                  padding: const EdgeInsets.symmetric(
                    horizontal: AppSpacing.l,
                  ),
                  children: [
                    _StateChip(
                      label: 'All states',
                      selected: _stateFilter == null,
                      onTap: () => setState(() => _stateFilter = null),
                    ),
                    const SizedBox(width: AppSpacing.s),
                    for (final s in sorted) ...[
                      _StateChip(
                        label: s,
                        selected: _stateFilter == s,
                        onTap: () => setState(() => _stateFilter = s),
                      ),
                      const SizedBox(width: AppSpacing.s),
                    ],
                  ],
                ),
              );
            },
            orElse: () => const SizedBox.shrink(),
          ),
          Expanded(
            child: providersAsync.when(
              loading: () => const Center(
                child: CircularProgressIndicator(
                  color: AppColors.postbookPrimary,
                ),
              ),
              error: (_, _) => Center(
                child: Text(
                  'Could not load providers.',
                  style: AppTextStyles.bodySmall.copyWith(
                    color: AppColors.statusError,
                  ),
                ),
              ),
              data: (list) {
                final filtered = _query.isEmpty
                    ? list
                    : list
                        .where(
                          (p) =>
                              p.name.toLowerCase().contains(_query) ||
                              p.shortName.toLowerCase().contains(_query),
                        )
                        .toList();
                if (filtered.isEmpty) {
                  return Center(
                    child: Text(
                      'No providers match.',
                      style: AppTextStyles.bodySmall,
                    ),
                  );
                }
                return ListView.separated(
                  padding: const EdgeInsets.symmetric(
                    horizontal: AppSpacing.l,
                    vertical: AppSpacing.s,
                  ),
                  itemCount: filtered.length,
                  separatorBuilder: (_, _) =>
                      const SizedBox(height: AppSpacing.s),
                  itemBuilder: (_, i) => _ProviderRow(
                    provider: filtered[i],
                    accounts: accounts.maybeWhen(
                      data: (l) => l,
                      orElse: () => const <BillAccount>[],
                    ),
                  ),
                );
              },
            ),
          ),
        ],
      ),
    );
  }
}

class _StateChip extends StatelessWidget {
  const _StateChip({
    required this.label,
    required this.selected,
    required this.onTap,
  });

  final String label;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.symmetric(
          horizontal: AppSpacing.l,
          vertical: AppSpacing.s,
        ),
        decoration: BoxDecoration(
          color: selected
              ? AppColors.postbookPrimary.withAlpha(40)
              : AppColors.bgTertiary,
          border: Border.all(
            color: selected
                ? AppColors.postbookPrimary
                : AppColors.borderSubtle,
          ),
          borderRadius: BorderRadius.circular(999),
        ),
        child: Text(
          label,
          style: AppTextStyles.labelSmall.copyWith(
            color: selected
                ? AppColors.postbookPrimary
                : AppColors.textSecondary,
          ),
        ),
      ),
    );
  }
}

class _ProviderRow extends StatelessWidget {
  const _ProviderRow({required this.provider, required this.accounts});

  final BillProvider provider;
  final List<BillAccount> accounts;

  @override
  Widget build(BuildContext context) {
    BillAccount? existing;
    for (final a in accounts) {
      if (a.providerId == provider.id) {
        existing = a;
        break;
      }
    }
    final hasExisting = existing != null;
    return InkWell(
      borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
      onTap: () {
        if (hasExisting) {
          context.push('/billpay/account/${existing!.id}');
        } else {
          context.push(
            '/billpay/add-account?providerId=${provider.id}',
          );
        }
      },
      child: Container(
        padding: const EdgeInsets.all(AppSpacing.l),
        decoration: BoxDecoration(
          color: AppColors.bgTertiary,
          borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Row(
          children: [
            Container(
              width: 40,
              height: 40,
              decoration: BoxDecoration(
                color: AppColors.bgSecondary,
                borderRadius: BorderRadius.circular(8),
                border: Border.all(color: AppColors.borderSubtle),
              ),
              clipBehavior: Clip.antiAlias,
              child: (provider.logoUrl == null || provider.logoUrl!.isEmpty)
                  ? const Icon(
                      Icons.receipt_long_rounded,
                      color: AppColors.textTertiary,
                      size: 20,
                    )
                  : Image.network(
                      provider.logoUrl!,
                      fit: BoxFit.cover,
                      errorBuilder: (_, _, _) => const Icon(
                        Icons.receipt_long_rounded,
                        color: AppColors.textTertiary,
                        size: 20,
                      ),
                    ),
            ),
            const SizedBox(width: AppSpacing.l),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(provider.name, style: AppTextStyles.h3),
                  if (hasExisting) ...[
                    const SizedBox(height: 2),
                    Text(
                      'Saved · ${existing.maskedIdentifier}',
                      style: AppTextStyles.bodySmall.copyWith(
                        color: AppColors.statusSuccess,
                      ),
                    ),
                  ] else if (provider.billFetchSupported) ...[
                    const SizedBox(height: 2),
                    Text(
                      'Auto-fetch supported',
                      style: AppTextStyles.bodySmall,
                    ),
                  ],
                ],
              ),
            ),
            const Icon(
              Icons.arrow_forward_ios_rounded,
              color: AppColors.textTertiary,
              size: 14,
            ),
          ],
        ),
      ),
    );
  }
}

