import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/features/services/models/service_app.dart';
import 'package:atpost_app/features/services/widgets/service_icon.dart';
import 'package:atpost_app/features/services/widgets/service_status_badge.dart';
import 'package:flutter/material.dart';

enum ServiceCardVariant { grid, list, featured }

/// Tappable card representing a [ServiceApp]. Three variants:
/// - `grid` — square tile used by the Quick Actions grid.
/// - `list` — full-width row used by the long list and search results.
/// - `featured` — highlighted card with description, used in the featured rail.
class ServiceCard extends StatelessWidget {
  const ServiceCard({
    super.key,
    required this.app,
    required this.onTap,
    this.variant = ServiceCardVariant.grid,
  });

  final ServiceApp app;
  final ValueChanged<ServiceApp> onTap;
  final ServiceCardVariant variant;

  @override
  Widget build(BuildContext context) {
    switch (variant) {
      case ServiceCardVariant.featured:
        return _buildFeatured();
      case ServiceCardVariant.list:
        return _buildList();
      case ServiceCardVariant.grid:
        return _buildGrid();
    }
  }

  Widget _buildGrid() {
    final dim = !app.status.isOpenable;
    final showBadge = app.status != ServiceStatus.active;
    return Opacity(
      opacity: dim ? 0.65 : 1,
      child: InkWell(
        onTap: () => onTap(app),
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        child: Padding(
          padding: const EdgeInsets.symmetric(vertical: 6, horizontal: 4),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              _IconBadge(app: app, size: 50, iconSize: 24),
              const SizedBox(height: 6),
              Text(
                app.name,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                textAlign: TextAlign.center,
                style: AppTextStyles.labelSmall.copyWith(
                  color: AppColors.textPrimary,
                  fontWeight: FontWeight.w600,
                ),
              ),
              if (showBadge) ...[
                const SizedBox(height: 2),
                _CompactStatusPill(status: app.status),
              ],
            ],
          ),
        ),
      ),
    );
  }

  Widget _buildList() {
    final dim = !app.status.isOpenable;
    return Opacity(
      opacity: dim ? 0.75 : 1,
      child: InkWell(
        onTap: () => onTap(app),
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        child: Padding(
          padding: const EdgeInsets.symmetric(vertical: 10, horizontal: 8),
          child: Row(
            children: [
              _IconBadge(app: app, size: 48, iconSize: 24),
              const SizedBox(width: 12),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      app.name,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: AppTextStyles.h3,
                    ),
                    const SizedBox(height: 2),
                    Text(
                      app.shortDescription,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: AppTextStyles.bodySmall,
                    ),
                  ],
                ),
              ),
              const SizedBox(width: 12),
              ServiceStatusBadge(status: app.status),
            ],
          ),
        ),
      ),
    );
  }

  Widget _buildFeatured() {
    return InkWell(
      onTap: () => onTap(app),
      borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
      child: Container(
        width: 220,
        padding: const EdgeInsets.all(14),
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                _IconBadge(app: app, size: 44, iconSize: 22),
                const Spacer(),
                ServiceStatusBadge(status: app.status),
              ],
            ),
            const SizedBox(height: 12),
            Text(
              app.name,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: AppTextStyles.h3,
            ),
            const SizedBox(height: 4),
            Text(
              app.shortDescription,
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
              style: AppTextStyles.bodySmall,
            ),
            if (app.isVerified) ...[
              const SizedBox(height: 10),
              Row(
                children: [
                  Icon(
                    Icons.verified_rounded,
                    size: 14,
                    color: AppColors.posttubePrimary,
                  ),
                  const SizedBox(width: 4),
                  Text(
                    'Verified by VChat',
                    style: AppTextStyles.labelTiny.copyWith(
                      color: AppColors.posttubePrimary,
                    ),
                  ),
                ],
              ),
            ],
          ],
        ),
      ),
    );
  }
}

/// Tight pill used inside the 4-col grid tile (smaller than [ServiceStatusBadge]
/// to keep the tile under its 101.6px height budget). Only rendered for
/// non-active statuses so active tiles stay clean.
class _CompactStatusPill extends StatelessWidget {
  const _CompactStatusPill({required this.status});

  final ServiceStatus status;

  @override
  Widget build(BuildContext context) {
    final (bg, fg) = _palette(status);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
      ),
      child: Text(
        status.label,
        style: TextStyle(
          fontSize: 9,
          fontWeight: FontWeight.w700,
          color: fg,
          letterSpacing: 0.3,
          height: 1.1,
        ),
      ),
    );
  }

  (Color, Color) _palette(ServiceStatus s) {
    switch (s) {
      case ServiceStatus.active:
        return (AppColors.statusSuccess.withValues(alpha: 0.18),
            AppColors.statusSuccess);
      case ServiceStatus.beta:
        return (AppColors.accentPurple.withValues(alpha: 0.18),
            AppColors.accentPurple);
      case ServiceStatus.comingSoon:
        return (AppColors.glassBg, AppColors.textTertiary);
      case ServiceStatus.disabled:
        return (AppColors.statusError.withValues(alpha: 0.16),
            AppColors.statusError);
    }
  }
}

class _IconBadge extends StatelessWidget {
  const _IconBadge({
    required this.app,
    required this.size,
    required this.iconSize,
  });

  final ServiceApp app;
  final double size;
  final double iconSize;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: size,
      height: size,
      decoration: BoxDecoration(
        color: app.accentColor.withValues(alpha: 0.16),
        borderRadius: BorderRadius.circular(size / 3.5),
      ),
      alignment: Alignment.center,
      child: Icon(
        iconForServiceName(app.iconName),
        size: iconSize,
        color: app.accentColor,
      ),
    );
  }
}
