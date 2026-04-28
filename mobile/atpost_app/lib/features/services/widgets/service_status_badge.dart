import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/features/services/models/service_app.dart';
import 'package:flutter/material.dart';

class ServiceStatusBadge extends StatelessWidget {
  const ServiceStatusBadge({super.key, required this.status});

  final ServiceStatus status;

  @override
  Widget build(BuildContext context) {
    final (bg, fg) = _palette(status);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
      ),
      child: Text(
        status.label,
        style: AppTextStyles.labelTiny.copyWith(
          color: fg,
          letterSpacing: 0.4,
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
