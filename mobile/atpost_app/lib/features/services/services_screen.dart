import 'dart:async';

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/features/services/data/service_registry.dart';
import 'package:atpost_app/features/services/models/service_app.dart';
import 'package:atpost_app/features/services/service_app_launcher.dart';
import 'package:atpost_app/features/services/widgets/service_card.dart';
import 'package:atpost_app/providers/user_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// Mini App Center at /services. Curated catalog of internal Postbook
/// modules with quick actions, a featured rail, category filters, and a
/// searchable list.
class ServicesScreen extends ConsumerStatefulWidget {
  const ServicesScreen({super.key});

  @override
  ConsumerState<ServicesScreen> createState() => _ServicesScreenState();
}

class _ServicesScreenState extends ConsumerState<ServicesScreen> {
  final _searchController = TextEditingController();
  Timer? _debounce;
  String _query = '';
  ServiceCategory? _category;

  @override
  void dispose() {
    _debounce?.cancel();
    _searchController.dispose();
    super.dispose();
  }

  void _onSearchChanged(String value) {
    _debounce?.cancel();
    _debounce = Timer(const Duration(milliseconds: 250), () {
      if (mounted) setState(() => _query = value.trim());
    });
  }

  Future<void> _open(ServiceApp app) async {
    await openServiceApp(context, ref, app);
  }

  List<ServiceApp> _filtered() {
    Iterable<ServiceApp> result = _query.isEmpty
        ? ServiceRegistry.all()
        : ServiceRegistry.search(_query);
    if (_category != null) {
      result = result.where((a) => a.category == _category);
    }
    final list = result.toList();
    list.sort((a, b) {
      // Active first, then by sortOrder.
      final ao = a.status.isOpenable ? 0 : 1;
      final bo = b.status.isOpenable ? 0 : 1;
      if (ao != bo) return ao.compareTo(bo);
      return a.sortOrder.compareTo(b.sortOrder);
    });
    return list;
  }

  @override
  Widget build(BuildContext context) {
    final filtered = _filtered();
    final featured = ServiceRegistry.featured();
    final quick = ServiceRegistry.active().take(8).toList();

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        leading: context.canPop()
            ? IconButton(
                icon: const Icon(Icons.arrow_back_rounded),
                color: AppColors.textPrimary,
                onPressed: () => context.pop(),
              )
            : null,
        automaticallyImplyLeading: false,
        title: Text('Services', style: AppTextStyles.h2),
      ),
      body: ListView(
        padding: const EdgeInsets.only(bottom: 32),
        children: [
          _Greeting(),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 18),
            child: _SearchField(
              controller: _searchController,
              onChanged: _onSearchChanged,
            ),
          ),
          if (_query.isEmpty && _category == null) ...[
            const SizedBox(height: 24),
            _SectionHeader(title: 'Quick actions'),
            _QuickActionsGrid(apps: quick, onTap: _open),
            const SizedBox(height: 24),
            if (featured.isNotEmpty) ...[
              _SectionHeader(title: 'Featured'),
              _FeaturedRail(apps: featured, onTap: _open),
              const SizedBox(height: 24),
            ],
          ],
          _SectionHeader(title: 'Browse'),
          _CategoryChips(
            selected: _category,
            onChange: (c) => setState(() => _category = c),
          ),
          const SizedBox(height: 8),
          if (filtered.isEmpty)
            Padding(
              padding: const EdgeInsets.symmetric(
                  horizontal: 18, vertical: 28),
              child: Center(
                child: Text(
                  'No services match that filter.',
                  style: AppTextStyles.body
                      .copyWith(color: AppColors.textMuted),
                ),
              ),
            )
          else
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 10),
              child: Column(
                children: [
                  for (final app in filtered)
                    ServiceCard(
                      app: app,
                      onTap: _open,
                      variant: ServiceCardVariant.list,
                    ),
                ],
              ),
            ),
        ],
      ),
    );
  }
}

class _Greeting extends ConsumerWidget {
  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final me = ref.watch(currentUserProvider);
    final name = me.maybeWhen(
      data: (u) {
        final dn = u.displayName.trim();
        if (dn.isEmpty) return '';
        return dn.split(' ').first;
      },
      orElse: () => '',
    );
    final greeting = name.isEmpty ? 'Hello' : 'Hi, $name';
    return Padding(
      padding: const EdgeInsets.fromLTRB(18, 8, 18, 14),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(greeting, style: AppTextStyles.h1),
          const SizedBox(height: 4),
          Text(
            'Discover AtPost services and mini-apps.',
            style: AppTextStyles.body.copyWith(color: AppColors.textTertiary),
          ),
        ],
      ),
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
        style: AppTextStyles.body
            .copyWith(color: AppColors.textPrimary, height: 1.2),
        decoration: InputDecoration(
          hintText: 'Search services',
          hintStyle:
              AppTextStyles.body.copyWith(color: AppColors.textMuted),
          prefixIcon: const Icon(Icons.search_rounded,
              color: AppColors.textMuted, size: 20),
          border: InputBorder.none,
          contentPadding:
              const EdgeInsets.symmetric(vertical: 12, horizontal: 4),
        ),
      ),
    );
  }
}

class _SectionHeader extends StatelessWidget {
  const _SectionHeader({required this.title});

  final String title;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(18, 4, 18, 10),
      child: Text(title, style: AppTextStyles.h3),
    );
  }
}

class _QuickActionsGrid extends StatelessWidget {
  const _QuickActionsGrid({required this.apps, required this.onTap});

  final List<ServiceApp> apps;
  final ValueChanged<ServiceApp> onTap;

  @override
  Widget build(BuildContext context) {
    if (apps.isEmpty) return const SizedBox.shrink();
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 10),
      child: GridView.builder(
        shrinkWrap: true,
        physics: const NeverScrollableScrollPhysics(),
        gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(
          crossAxisCount: 4,
          mainAxisSpacing: 4,
          crossAxisSpacing: 4,
          childAspectRatio: 0.85,
        ),
        itemCount: apps.length,
        itemBuilder: (_, i) => ServiceCard(
          app: apps[i],
          onTap: onTap,
        ),
      ),
    );
  }
}

class _FeaturedRail extends StatelessWidget {
  const _FeaturedRail({required this.apps, required this.onTap});

  final List<ServiceApp> apps;
  final ValueChanged<ServiceApp> onTap;

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      height: 168,
      child: ListView.separated(
        scrollDirection: Axis.horizontal,
        padding: const EdgeInsets.symmetric(horizontal: 18),
        itemCount: apps.length,
        separatorBuilder: (_, _) => const SizedBox(width: 12),
        itemBuilder: (_, i) => ServiceCard(
          app: apps[i],
          onTap: onTap,
          variant: ServiceCardVariant.featured,
        ),
      ),
    );
  }
}

class _CategoryChips extends StatelessWidget {
  const _CategoryChips({required this.selected, required this.onChange});

  final ServiceCategory? selected;
  final ValueChanged<ServiceCategory?> onChange;

  @override
  Widget build(BuildContext context) {
    return SingleChildScrollView(
      scrollDirection: Axis.horizontal,
      padding: const EdgeInsets.symmetric(horizontal: 14),
      child: Row(
        children: [
          _Chip(
            label: 'All',
            active: selected == null,
            onTap: () => onChange(null),
          ),
          for (final c in ServiceCategory.values)
            _Chip(
              label: c.label,
              active: selected == c,
              onTap: () => onChange(c),
            ),
        ],
      ),
    );
  }
}

class _Chip extends StatelessWidget {
  const _Chip({
    required this.label,
    required this.active,
    required this.onTap,
  });

  final String label;
  final bool active;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 4),
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 8),
          decoration: BoxDecoration(
            color: active ? AppColors.accentPurple : AppColors.bgCard,
            border: Border.all(
              color: active
                  ? AppColors.accentPurple
                  : AppColors.borderSubtle,
            ),
            borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
          ),
          child: Text(
            label,
            style: AppTextStyles.label.copyWith(
              color: active ? Colors.white : AppColors.textSecondary,
            ),
          ),
        ),
      ),
    );
  }
}
