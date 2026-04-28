import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/features/services/models/service_app.dart';
import 'package:atpost_app/features/services/widgets/service_icon.dart';
import 'package:atpost_app/features/services/widgets/service_status_badge.dart';
import 'package:flutter/material.dart';

class ComingSoonScreen extends StatelessWidget {
  const ComingSoonScreen({super.key, required this.app});

  final ServiceApp app;

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 32),
      children: [
        const SizedBox(height: 24),
        Center(
          child: Container(
            width: 96,
            height: 96,
            decoration: BoxDecoration(
              color: app.accentColor.withValues(alpha: 0.16),
              borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
            ),
            alignment: Alignment.center,
            child: Icon(
              iconForServiceName(app.iconName),
              size: 48,
              color: app.accentColor,
            ),
          ),
        ),
        const SizedBox(height: 20),
        Center(
          child: Text(
            app.name,
            style: AppTextStyles.h1,
            textAlign: TextAlign.center,
          ),
        ),
        const SizedBox(height: 8),
        Center(child: ServiceStatusBadge(status: app.status)),
        const SizedBox(height: 18),
        Text(
          app.longDescription ?? app.shortDescription,
          style: AppTextStyles.body,
          textAlign: TextAlign.center,
        ),
        const SizedBox(height: 32),
        ElevatedButton.icon(
          onPressed: () {
            ScaffoldMessenger.of(context).showSnackBar(
              SnackBar(
                content: Text(
                  "We'll let you know when ${app.name} launches.",
                ),
                backgroundColor: AppColors.bgTertiary,
              ),
            );
          },
          icon: const Icon(Icons.notifications_active_rounded, size: 18),
          label: const Text('Notify Me'),
          style: ElevatedButton.styleFrom(
            backgroundColor: app.accentColor,
            foregroundColor: Colors.white,
            padding: const EdgeInsets.symmetric(vertical: 14),
            shape: RoundedRectangleBorder(
              borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
            ),
            elevation: 0,
          ),
        ),
      ],
    );
  }
}
