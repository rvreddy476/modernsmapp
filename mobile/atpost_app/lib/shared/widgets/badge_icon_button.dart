import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/shared/widgets/glass_icon_button.dart';
import 'package:flutter/material.dart';

class BadgeIconButton extends StatelessWidget {
  const BadgeIconButton({
    super.key,
    required this.icon,
    this.badgeCount = 0,
    this.onPressed,
  });

  final IconData icon;
  final int badgeCount;
  final VoidCallback? onPressed;

  @override
  Widget build(BuildContext context) {
    return Stack(
      clipBehavior: Clip.none,
      children: [
        GlassIconButton(icon: icon, onPressed: onPressed),
        if (badgeCount > 0)
          Positioned(
            right: -2,
            top: -2,
            child: Container(
              height: 16,
              constraints: const BoxConstraints(minWidth: 16),
              padding: const EdgeInsets.symmetric(horizontal: 4),
              decoration: BoxDecoration(
                color: AppColors.postbookPrimary,
                borderRadius: BorderRadius.circular(999),
                border: Border.all(color: AppColors.bgPrimary, width: 2),
              ),
              child: Center(
                child: Text(
                  badgeCount > 99 ? '99+' : '$badgeCount',
                  style: AppTextStyles.labelTiny.copyWith(color: Colors.white),
                ),
              ),
            ),
          ),
      ],
    );
  }
}

