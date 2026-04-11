import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/mini_app.dart';
import 'package:atpost_app/features/mini_apps/mini_app_permission_prompt.dart';
import 'package:atpost_app/providers/mini_apps_provider.dart';
import 'package:atpost_app/shared/widgets/glass_icon_button.dart';
import 'package:flutter/material.dart';
import 'package:flutter_animate/flutter_animate.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// A modern, elegant Mini Apps store designed for production scale.
/// Features: Immersive UI, optimistic installations, and category-based discovery.
class MiniAppsScreen extends ConsumerStatefulWidget {
  const MiniAppsScreen({super.key});

  @override
  ConsumerState<MiniAppsScreen> createState() => _MiniAppsScreenState();
}

class _MiniAppsScreenState extends ConsumerState<MiniAppsScreen> {
  bool _showInstalledOnly = false;

  final List<Map<String, String>> _categories = [
    {'label': 'All', 'value': 'all'},
    {'label': 'Games', 'value': 'games'},
    {'label': 'Booking', 'value': 'booking'},
    {'label': 'Learning', 'value': 'learning'},
    {'label': 'Shopping', 'value': 'shopping'},
    {'label': 'Tools', 'value': 'tools'},
    {'label': 'Entertainment', 'value': 'entertainment'},
  ];

  @override
  Widget build(BuildContext context) {
    final state = ref.watch(miniAppsProvider);

    return Scaffold(
      backgroundColor: Colors.black,
      body: Container(
        decoration: const BoxDecoration(
          gradient: LinearGradient(
            begin: Alignment.topLeft,
            end: Alignment.bottomRight,
            colors: [Color(0xFF0F111A), Color(0xFF090A11), Color(0xFF1A1D2E)],
          ),
        ),
        child: SafeArea(
          child: Column(
            children: [
              _buildHeader(
                context,
                state.valueOrNull?.installedApps.length ?? 0,
              ),
              _buildCategoryBar(state.valueOrNull?.selectedCategory),
              Expanded(
                child: state.when(
                  loading: () => const Center(
                    child: CircularProgressIndicator(
                      color: AppColors.postbookPrimary,
                    ),
                  ),
                  error: (e, _) => _buildErrorState(),
                  data: (data) => _buildAppGrid(data),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  Widget _buildHeader(BuildContext context, int installedCount) {
    return Padding(
      padding: const EdgeInsets.all(16),
      child: Row(
        children: [
          GlassIconButton(
            icon: Icons.arrow_back_ios_new,
            tooltip: 'Back',
            onPressed: () => context.pop(),
          ),
          const SizedBox(width: 12),
          Text('App Store', style: AppTextStyles.h1),
          const Spacer(),
          GestureDetector(
            onTap: () =>
                setState(() => _showInstalledOnly = !_showInstalledOnly),
            child: Container(
              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
              decoration: BoxDecoration(
                color: _showInstalledOnly
                    ? AppColors.postbookPrimary.withValues(alpha: 0.2)
                    : Colors.white.withValues(alpha: 0.05),
                borderRadius: BorderRadius.circular(20),
                border: Border.all(
                  color: _showInstalledOnly
                      ? AppColors.postbookPrimary
                      : Colors.white10,
                ),
              ),
              child: Row(
                children: [
                  Icon(
                    _showInstalledOnly ? Icons.download_done : Icons.apps,
                    size: 16,
                    color: _showInstalledOnly
                        ? AppColors.postbookPrimary
                        : Colors.white70,
                  ),
                  const SizedBox(width: 6),
                  Text(
                    _showInstalledOnly
                        ? 'Installed $installedCount'
                        : 'Explore',
                    style: AppTextStyles.labelSmall.copyWith(
                      color: _showInstalledOnly
                          ? AppColors.postbookPrimary
                          : Colors.white70,
                      fontWeight: FontWeight.bold,
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

  Widget _buildCategoryBar(String? selected) {
    return Container(
      height: 50,
      margin: const EdgeInsets.only(bottom: 8),
      child: ListView.builder(
        scrollDirection: Axis.horizontal,
        padding: const EdgeInsets.symmetric(horizontal: 16),
        itemCount: _categories.length,
        itemBuilder: (context, index) {
          final cat = _categories[index];
          final isSelected =
              (selected == null && cat['value'] == 'all') ||
              (selected == cat['value']);
          return Padding(
            padding: const EdgeInsets.only(right: 8),
            child: ChoiceChip(
              label: Text(cat['label']!),
              selected: isSelected,
              onSelected: (_) {
                ref
                    .read(miniAppsProvider.notifier)
                    .setCategory(cat['value'] == 'all' ? null : cat['value']);
              },
              selectedColor: AppColors.postbookPrimary.withValues(alpha: 0.2),
              backgroundColor: Colors.white.withValues(alpha: 0.03),
              labelStyle: TextStyle(
                color: isSelected ? AppColors.postbookPrimary : Colors.white38,
                fontWeight: isSelected ? FontWeight.bold : FontWeight.normal,
                fontSize: 13,
              ),
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(20),
              ),
              side: BorderSide(
                color: isSelected ? AppColors.postbookPrimary : Colors.white10,
              ),
              showCheckmark: false,
            ),
          );
        },
      ),
    );
  }

  Widget _buildAppGrid(MiniAppsState data) {
    final apps = _showInstalledOnly ? data.installedApps : data.allApps;

    if (apps.isEmpty) {
      return Center(
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            const Icon(Icons.cloud_off, size: 48, color: Colors.white10),
            const SizedBox(height: 16),
            Text(
              _showInstalledOnly
                  ? 'No apps installed yet'
                  : 'No apps found in this category',
              style: AppTextStyles.bodySmall.copyWith(color: Colors.white24),
            ),
          ],
        ),
      );
    }

    return GridView.builder(
      padding: const EdgeInsets.all(16),
      gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(
        crossAxisCount: 2,
        mainAxisSpacing: 16,
        crossAxisSpacing: 16,
        childAspectRatio: 0.75,
      ),
      itemCount: apps.length,
      itemBuilder: (context, index) => _MiniAppGlassCard(app: apps[index]),
    );
  }

  Widget _buildErrorState() {
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          const Icon(Icons.error_outline, color: Colors.redAccent, size: 40),
          const SizedBox(height: 16),
          Text('Failed to load apps', style: AppTextStyles.body),
          TextButton(
            onPressed: () => ref.refresh(miniAppsProvider),
            child: const Text('Retry'),
          ),
        ],
      ),
    );
  }
}

class _MiniAppGlassCard extends ConsumerWidget {
  final MiniApp app;
  const _MiniAppGlassCard({required this.app});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return RepaintBoundary(
          child: GestureDetector(
            onTap: () => context.push('/apps/${app.id}'),
            child: Container(
              padding: const EdgeInsets.all(12),
              decoration: BoxDecoration(
                color: Colors.white.withValues(alpha: 0.03),
                borderRadius: BorderRadius.circular(24),
                border: Border.all(color: Colors.white.withValues(alpha: 0.05)),
              ),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  _buildAppIcon(),
                  const SizedBox(height: 12),
                  Text(
                    app.name,
                    style: AppTextStyles.label.copyWith(
                      fontWeight: FontWeight.bold,
                      fontSize: 15,
                    ),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                  const SizedBox(height: 4),
                  Text(
                    app.description,
                    style: AppTextStyles.labelTiny.copyWith(
                      color: Colors.white38,
                    ),
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                  ),
                  const Spacer(),
                  _buildInstallButton(context, ref),
                ],
              ),
            ),
          ),
        )
        .animate()
        .fadeIn(duration: 300.ms)
        .scale(begin: const Offset(0.95, 0.95), end: const Offset(1, 1));
  }

  Widget _buildAppIcon() {
    return Container(
      width: 56,
      height: 56,
      decoration: BoxDecoration(
        color: Colors.white.withValues(alpha: 0.05),
        borderRadius: BorderRadius.circular(18),
        image: app.iconUrl != null
            ? DecorationImage(
                image: NetworkImage(app.iconUrl!),
                fit: BoxFit.cover,
              )
            : null,
      ),
      child: app.iconUrl == null
          ? Center(
              child: Text(
                app.name[0].toUpperCase(),
                style: const TextStyle(
                  color: Colors.white,
                  fontSize: 24,
                  fontWeight: FontWeight.bold,
                ),
              ),
            )
          : null,
    );
  }

  Widget _buildInstallButton(BuildContext context, WidgetRef ref) {
    return SizedBox(
      width: double.infinity,
      child: ElevatedButton(
        onPressed: () async {
          if (app.isInstalled) {
            context.push('/apps/sandbox/${app.id}');
            return;
          }

          final grantedPermissions = await showMiniAppPermissionPrompt(
            context: context,
            appName: app.name,
            requestedPermissions: app.permissions,
          );
          if (grantedPermissions == null) return;

          await ref
              .read(miniAppsProvider.notifier)
              .installApp(app.id, grantedPermissions: grantedPermissions);
        },
        style: ElevatedButton.styleFrom(
          backgroundColor: app.isInstalled
              ? Colors.white.withValues(alpha: 0.05)
              : AppColors.postbookPrimary,
          foregroundColor: app.isInstalled ? Colors.white70 : Colors.white,
          elevation: 0,
          padding: const EdgeInsets.symmetric(vertical: 8),
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(14),
            side: app.isInstalled
                ? BorderSide(color: Colors.white.withValues(alpha: 0.1))
                : BorderSide.none,
          ),
        ),
        child: Text(
          app.isInstalled ? 'Open' : 'Get',
          style: AppTextStyles.labelSmall.copyWith(fontWeight: FontWeight.bold),
        ),
      ),
    );
  }
}
