// Address book — Sprint 1.
//
// Lists addresses from `addressesProvider`. Default badge, edit/delete swipe
// actions, "Add new" CTA. When opened with `?picker=1` the screen acts as a
// picker for checkout — tapping an address pops with its id as the result.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/commerce.dart';
import 'package:atpost_app/data/repositories/commerce_repository.dart';
import 'package:atpost_app/providers/commerce_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class AddressBookScreen extends ConsumerWidget {
  const AddressBookScreen({super.key, this.pickerMode = false});

  final bool pickerMode;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final asyncAddrs = ref.watch(addressesProvider);
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text(
          pickerMode ? 'Pick address' : 'Saved addresses',
          style: AppTextStyles.h2,
        ),
      ),
      body: asyncAddrs.when(
        loading: () => const Center(
          child: CircularProgressIndicator(color: AppColors.postbookPrimary),
        ),
        error: (e, _) => Center(
          child: Padding(
            padding: const EdgeInsets.all(AppSpacing.xxl),
            child: Text('Could not load addresses.\n$e',
                style: AppTextStyles.body, textAlign: TextAlign.center),
          ),
        ),
        data: (addrs) {
          if (addrs.isEmpty) {
            return Center(
              child: Padding(
                padding: const EdgeInsets.all(AppSpacing.xxl),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    const Icon(Icons.location_off_outlined,
                        size: 56, color: AppColors.textGhost),
                    const SizedBox(height: AppSpacing.l),
                    Text('No saved addresses',
                        style: AppTextStyles.h3),
                    const SizedBox(height: AppSpacing.s),
                    Text(
                      'Add an address to start placing orders',
                      style: AppTextStyles.body,
                    ),
                  ],
                ),
              ),
            );
          }
          return ListView.separated(
            padding: const EdgeInsets.all(AppSpacing.l),
            itemCount: addrs.length,
            separatorBuilder: (_, _) => const SizedBox(height: AppSpacing.m),
            itemBuilder: (ctx, i) => _AddressTile(
              address: addrs[i],
              pickerMode: pickerMode,
            ),
          );
        },
      ),
      floatingActionButton: FloatingActionButton.extended(
        onPressed: () => GoRouter.of(context).push('/commerce/addresses/new'),
        backgroundColor: AppColors.postbookPrimary,
        icon: const Icon(Icons.add),
        label: const Text('Add new'),
      ),
    );
  }
}

class _AddressTile extends ConsumerWidget {
  const _AddressTile({required this.address, required this.pickerMode});

  final Address address;
  final bool pickerMode;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return InkWell(
      onTap: () async {
        if (pickerMode) {
          GoRouter.of(context).pop(address.id);
          return;
        }
        // Non-picker tap → set as default.
        await ref
            .read(commerceRepositoryProvider)
            .setDefaultAddress(address.id);
        ref.invalidate(addressesProvider);
      },
      borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
      child: Container(
        padding: const EdgeInsets.all(AppSpacing.l),
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
          border: Border.all(
            color: address.isDefault
                ? AppColors.postbookPrimary
                : AppColors.borderSubtle,
          ),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Container(
                  padding: const EdgeInsets.symmetric(
                      horizontal: AppSpacing.m, vertical: 2),
                  decoration: BoxDecoration(
                    color: AppColors.bgSecondary,
                    borderRadius:
                        BorderRadius.circular(AppSpacing.radiusFull),
                  ),
                  child: Text(address.label, style: AppTextStyles.labelSmall),
                ),
                const SizedBox(width: AppSpacing.s),
                if (address.isDefault)
                  Container(
                    padding: const EdgeInsets.symmetric(
                        horizontal: AppSpacing.m, vertical: 2),
                    decoration: BoxDecoration(
                      color: AppColors.postbookPrimary,
                      borderRadius:
                          BorderRadius.circular(AppSpacing.radiusFull),
                    ),
                    child: Text(
                      'Default',
                      style: AppTextStyles.labelSmall.copyWith(
                        color: Colors.white,
                      ),
                    ),
                  ),
                const Spacer(),
                PopupMenuButton<String>(
                  icon: const Icon(Icons.more_vert,
                      color: AppColors.textTertiary),
                  onSelected: (value) async {
                    final repo = ref.read(commerceRepositoryProvider);
                    switch (value) {
                      case 'edit':
                        if (!context.mounted) return;
                        GoRouter.of(context).push(
                          '/commerce/addresses/new',
                          extra: address,
                        );
                        break;
                      case 'default':
                        await repo.setDefaultAddress(address.id);
                        ref.invalidate(addressesProvider);
                        break;
                      case 'delete':
                        await repo.deleteAddress(address.id);
                        ref.invalidate(addressesProvider);
                        break;
                    }
                  },
                  itemBuilder: (ctx) => [
                    const PopupMenuItem(value: 'edit', child: Text('Edit')),
                    if (!address.isDefault)
                      const PopupMenuItem(
                          value: 'default', child: Text('Set as default')),
                    const PopupMenuItem(
                        value: 'delete', child: Text('Delete')),
                  ],
                ),
              ],
            ),
            const SizedBox(height: AppSpacing.s),
            Text(address.fullName, style: AppTextStyles.label),
            const SizedBox(height: 2),
            Text(
              [
                address.line1,
                if (address.line2 != null) address.line2,
                address.city,
                '${address.state} ${address.postalCode}',
              ].whereType<String>().join(', '),
              style: AppTextStyles.bodySmall,
            ),
            const SizedBox(height: 2),
            Text('Phone: ${address.phone}',
                style: AppTextStyles.bodySmall),
          ],
        ),
      ),
    );
  }
}
