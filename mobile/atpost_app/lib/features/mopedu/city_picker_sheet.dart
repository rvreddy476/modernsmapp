// Mopedu — city picker bottom sheet.
//
// Lists supported cities returned by `riderCitiesProvider`. Tapping a row
// persists the selection through `selectedCityProvider` (secure storage)
// and fires the `mopedu.city.changed` telemetry event.
//
// In v1 the supported list is short (Bengaluru / Mumbai / Delhi). We use
// the city `id` returned from the backend so a future server-side
// expansion just shows up here without code changes.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/mopedu.dart';
import 'package:atpost_app/providers/mopedu_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class CityPickerSheet extends ConsumerWidget {
  const CityPickerSheet({super.key});

  static Future<String?> show(BuildContext context) {
    return showModalBottomSheet<String?>(
      context: context,
      backgroundColor: AppColors.bgSecondary,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      builder: (_) => const CityPickerSheet(),
    );
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final cities = ref.watch(riderCitiesProvider);
    final selected = ref.watch(selectedCityProvider);

    return SafeArea(
      top: false,
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Text('Choose city', style: AppTextStyles.h2),
                const Spacer(),
                IconButton(
                  icon: const Icon(Icons.close, color: AppColors.textTertiary),
                  onPressed: () => Navigator.of(context).pop(),
                ),
              ],
            ),
            const SizedBox(height: 4),
            Text(
              'Mopedu is currently live in select cities. More are on the way.',
              style: AppTextStyles.bodySmall,
            ),
            const SizedBox(height: 12),
            cities.when(
              data: (list) => Column(
                children: [
                  for (final c in list)
                    _CityRow(
                      city: c,
                      isSelected: selected == c.id,
                      onTap: () async {
                        await ref
                            .read(selectedCityProvider.notifier)
                            .select(c.id);
                        if (context.mounted) Navigator.of(context).pop(c.id);
                      },
                    ),
                ],
              ),
              loading: () => const Padding(
                padding: EdgeInsets.all(24),
                child: Center(child: CircularProgressIndicator()),
              ),
              error: (e, _) => Padding(
                padding: const EdgeInsets.all(16),
                child: Text(
                  'Could not load cities. Pull to retry.',
                  style: AppTextStyles.bodySmall,
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _CityRow extends StatelessWidget {
  const _CityRow({
    required this.city,
    required this.isSelected,
    required this.onTap,
  });

  final RiderCity city;
  final bool isSelected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Material(
      color: Colors.transparent,
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 12),
          decoration: BoxDecoration(
            color: isSelected
                ? AppColors.postbookPrimary.withValues(alpha: 0.10)
                : Colors.transparent,
            borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
            border: Border.all(
              color: isSelected
                  ? AppColors.postbookPrimary.withValues(alpha: 0.4)
                  : AppColors.borderSubtle,
            ),
          ),
          margin: const EdgeInsets.only(bottom: 8),
          child: Row(
            children: [
              const Icon(
                Icons.location_city,
                color: AppColors.textTertiary,
                size: 18,
              ),
              const SizedBox(width: 10),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(city.name, style: AppTextStyles.label),
                    Text(
                      '${city.state}, ${city.country}',
                      style: AppTextStyles.bodySmall,
                    ),
                  ],
                ),
              ),
              if (isSelected)
                const Icon(
                  Icons.check_circle,
                  color: AppColors.postbookPrimary,
                  size: 18,
                ),
            ],
          ),
        ),
      ),
    );
  }
}
