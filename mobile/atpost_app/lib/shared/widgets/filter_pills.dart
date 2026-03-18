import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:flutter/material.dart';

class FilterPills extends StatelessWidget {
  const FilterPills({
    super.key,
    required this.labels,
    required this.activeIndex,
    required this.onChanged,
  });

  final List<String> labels;
  final int activeIndex;
  final ValueChanged<int> onChanged;

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      height: 38,
      child: ListView.separated(
        scrollDirection: Axis.horizontal,
        itemBuilder: (context, index) {
          final isActive = index == activeIndex;
          return Semantics(
            selected: isActive,
            label: labels[index],
            button: true,
            child: GestureDetector(
              onTap: () => onChanged(index),
              child: AnimatedContainer(
                duration: const Duration(milliseconds: 250),
                curve: Curves.easeOut,
                padding: const EdgeInsets.symmetric(horizontal: 18, vertical: 8),
                decoration: BoxDecoration(
                  borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
                  gradient: isActive ? AppColors.postbookGradient : null,
                  color: isActive ? null : AppColors.bgCard,
                  border: Border.all(color: AppColors.borderSubtle),
                  boxShadow: isActive
                      ? const [
                          BoxShadow(
                            color: Color(0x4DFF6B35),
                            blurRadius: 16,
                            offset: Offset(0, 4),
                          ),
                        ]
                      : const [],
                ),
                child: ExcludeSemantics(
                  child: Text(
                    labels[index],
                    style: AppTextStyles.label.copyWith(
                      color: isActive ? Colors.white : AppColors.textMuted,
                    ),
                  ),
                ),
              ),
            ),
          );
        },
        separatorBuilder: (context, index) => const SizedBox(width: 8),
        itemCount: labels.length,
      ),
    );
  }
}
