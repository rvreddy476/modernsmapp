import 'dart:async';

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/features/services/data/service_registry.dart';
import 'package:atpost_app/features/services/models/service_app.dart';
import 'package:atpost_app/features/services/service_app_launcher.dart';
import 'package:atpost_app/features/services/widgets/service_card.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Slides up a modal bottom sheet displaying the Mini App Center
/// (Quick Access + All Services). Used by the Explore button in the
/// shell bottom nav.
Future<void> showExploreBottomSheet(BuildContext context) {
  return showModalBottomSheet<void>(
    context: context,
    backgroundColor: Colors.transparent,
    isScrollControlled: true,
    useSafeArea: true,
    useRootNavigator: true,
    barrierColor: Colors.black.withValues(alpha: 0.55),
    builder: (sheetCtx) => const _ExploreBottomSheet(),
  );
}

class _ExploreBottomSheet extends ConsumerStatefulWidget {
  const _ExploreBottomSheet();

  @override
  ConsumerState<_ExploreBottomSheet> createState() =>
      _ExploreBottomSheetState();
}

class _ExploreBottomSheetState extends ConsumerState<_ExploreBottomSheet> {
  final _searchController = TextEditingController();
  Timer? _debounce;
  String _query = '';

  @override
  void dispose() {
    _debounce?.cancel();
    _searchController.dispose();
    super.dispose();
  }

  void _onSearchChanged(String value) {
    _debounce?.cancel();
    _debounce = Timer(const Duration(milliseconds: 220), () {
      if (mounted) setState(() => _query = value.trim());
    });
  }

  Future<void> _open(ServiceApp app) async {
    Navigator.of(context).pop();
    if (!mounted) return;
    await openServiceApp(context, ref, app);
  }

  List<ServiceApp> get _filtered {
    final all = _query.isEmpty
        ? ServiceRegistry.all()
        : ServiceRegistry.search(_query);
    final list = [...all];
    list.sort((a, b) {
      final ao = a.status.isOpenable ? 0 : 1;
      final bo = b.status.isOpenable ? 0 : 1;
      if (ao != bo) return ao.compareTo(bo);
      return a.sortOrder.compareTo(b.sortOrder);
    });
    return list;
  }

  @override
  Widget build(BuildContext context) {
    final quick = ServiceRegistry.active().take(4).toList();
    final media = MediaQuery.of(context);

    return DraggableScrollableSheet(
      initialChildSize: 0.85,
      minChildSize: 0.5,
      maxChildSize: 0.95,
      expand: false,
      builder: (context, scrollController) {
        return Container(
          decoration: BoxDecoration(
            color: AppColors.bgSecondary,
            borderRadius:
                const BorderRadius.vertical(top: Radius.circular(24)),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          padding: EdgeInsets.only(bottom: media.viewInsets.bottom),
          child: Column(
            children: [
              const SizedBox(height: 10),
              Container(
                width: 40,
                height: 4,
                decoration: BoxDecoration(
                  color: AppColors.borderMedium,
                  borderRadius: BorderRadius.circular(2),
                ),
              ),
              const SizedBox(height: 16),
              Padding(
                padding: const EdgeInsets.symmetric(horizontal: 20),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text('Explore', style: AppTextStyles.h1),
                    const SizedBox(height: 2),
                    Text(
                      'Everything inside VChat',
                      style: AppTextStyles.body
                          .copyWith(color: AppColors.textTertiary),
                    ),
                    const SizedBox(height: 14),
                    _SearchField(
                      controller: _searchController,
                      onChanged: _onSearchChanged,
                    ),
                  ],
                ),
              ),
              const SizedBox(height: 16),
              Expanded(
                child: ListView(
                  controller: scrollController,
                  padding: const EdgeInsets.fromLTRB(8, 0, 8, 24),
                  children: [
                    if (_query.isEmpty && quick.isNotEmpty) ...[
                      const _SectionLabel(text: 'QUICK ACCESS'),
                      _QuickAccessRow(apps: quick, onTap: _open),
                      const SizedBox(height: 18),
                    ],
                    _SectionLabel(
                      text: _query.isEmpty ? 'ALL SERVICES' : 'RESULTS',
                    ),
                    if (_filtered.isEmpty)
                      Padding(
                        padding: const EdgeInsets.symmetric(
                            horizontal: 12, vertical: 24),
                        child: Center(
                          child: Text(
                            'No services match "$_query".',
                            style: AppTextStyles.body
                                .copyWith(color: AppColors.textMuted),
                          ),
                        ),
                      )
                    else
                      Padding(
                        padding: const EdgeInsets.symmetric(horizontal: 4),
                        child: Column(
                          children: [
                            for (final app in _filtered)
                              Padding(
                                padding: const EdgeInsets.only(bottom: 4),
                                child: ServiceCard(
                                  app: app,
                                  onTap: _open,
                                  variant: ServiceCardVariant.list,
                                ),
                              ),
                          ],
                        ),
                      ),
                  ],
                ),
              ),
            ],
          ),
        );
      },
    );
  }
}

class _SearchField extends StatelessWidget {
  const _SearchField({required this.controller, required this.onChanged});

  final TextEditingController controller;
  final ValueChanged<String> onChanged;

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: TextField(
        controller: controller,
        onChanged: onChanged,
        style: AppTextStyles.body.copyWith(color: AppColors.textPrimary),
        decoration: InputDecoration(
          hintText: 'Search services…',
          hintStyle: AppTextStyles.body.copyWith(color: AppColors.textMuted),
          prefixIcon:
              const Icon(Icons.search_rounded, color: AppColors.textMuted),
          border: InputBorder.none,
          contentPadding: const EdgeInsets.symmetric(vertical: 12),
        ),
      ),
    );
  }
}

class _SectionLabel extends StatelessWidget {
  const _SectionLabel({required this.text});

  final String text;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
      child: Text(
        text,
        style: AppTextStyles.labelTiny.copyWith(
          color: AppColors.textTertiary,
          letterSpacing: 1.2,
        ),
      ),
    );
  }
}

class _QuickAccessRow extends StatelessWidget {
  const _QuickAccessRow({required this.apps, required this.onTap});

  final List<ServiceApp> apps;
  final ValueChanged<ServiceApp> onTap;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 4),
      child: Row(
        children: [
          for (final app in apps)
            Expanded(
              child: ServiceCard(
                app: app,
                onTap: onTap,
              ),
            ),
        ],
      ),
    );
  }
}
