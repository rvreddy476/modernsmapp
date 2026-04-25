import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:flutter/material.dart';

/// Single tab descriptor for the [FilterPills] widget.
class FilterPillItem {
  const FilterPillItem({
    required this.label,
    this.icon,
    this.activeGradient,
  });

  final String label;
  final IconData? icon;

  /// Optional gradient used when the tab is active. Falls back to the
  /// global [AppColors.postbookGradient] if null.
  final LinearGradient? activeGradient;
}

class FilterPills extends StatelessWidget {
  /// Convenience constructor for simple label-only pills (legacy usage).
  FilterPills({
    super.key,
    required List<String> labels,
    required this.activeIndex,
    required this.onChanged,
    this.fillWidth = false,
  }) : items = labels.map((l) => FilterPillItem(label: l)).toList();

  /// Rich constructor that accepts per-tab icons and active gradients.
  const FilterPills.rich({
    super.key,
    required this.items,
    required this.activeIndex,
    required this.onChanged,
    this.fillWidth = false,
  });

  final List<FilterPillItem> items;
  final int activeIndex;
  final ValueChanged<int> onChanged;

  /// When true, the pills stretch to fill the row using [Expanded] children
  /// so all tabs share equal width across the screen.
  final bool fillWidth;

  @override
  Widget build(BuildContext context) {
    if (fillWidth) {
      return SizedBox(
        height: 44,
        child: Row(
          children: List.generate(items.length, (i) {
            return Expanded(
              child: Padding(
                padding: EdgeInsets.only(right: i == items.length - 1 ? 0 : 8),
                child: _Pill(
                  item: items[i],
                  isActive: i == activeIndex,
                  onTap: () => onChanged(i),
                ),
              ),
            );
          }),
        ),
      );
    }

    return SizedBox(
      height: 38,
      child: ListView.separated(
        scrollDirection: Axis.horizontal,
        itemBuilder: (context, index) => _Pill(
          item: items[index],
          isActive: index == activeIndex,
          onTap: () => onChanged(index),
        ),
        separatorBuilder: (context, index) => const SizedBox(width: 8),
        itemCount: items.length,
      ),
    );
  }
}

class _Pill extends StatelessWidget {
  const _Pill({
    required this.item,
    required this.isActive,
    required this.onTap,
  });

  final FilterPillItem item;
  final bool isActive;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final gradient = item.activeGradient ?? AppColors.postbookGradient;
    final glowColor = (item.activeGradient?.colors.first) ?? AppColors.postbookPrimary;
    return Semantics(
      selected: isActive,
      label: item.label,
      button: true,
      child: GestureDetector(
        onTap: onTap,
        child: AnimatedContainer(
          duration: const Duration(milliseconds: 220),
          curve: Curves.easeOut,
          padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 8),
          decoration: BoxDecoration(
            borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
            gradient: isActive ? gradient : null,
            color: isActive ? null : AppColors.bgCard,
            border: Border.all(
              color: isActive
                  ? glowColor.withValues(alpha: 0.5)
                  : AppColors.borderSubtle,
            ),
            boxShadow: isActive
                ? [
                    BoxShadow(
                      color: glowColor.withValues(alpha: 0.35),
                      blurRadius: 14,
                      offset: const Offset(0, 4),
                    ),
                  ]
                : const [],
          ),
          child: Row(
            mainAxisAlignment: MainAxisAlignment.center,
            mainAxisSize: MainAxisSize.min,
            children: [
              if (item.icon != null) ...[
                Icon(
                  item.icon,
                  size: 16,
                  color: isActive ? Colors.white : AppColors.textMuted,
                ),
                const SizedBox(width: 6),
              ],
              Flexible(
                child: ExcludeSemantics(
                  child: Text(
                    item.label,
                    overflow: TextOverflow.ellipsis,
                    style: AppTextStyles.label.copyWith(
                      color: isActive ? Colors.white : AppColors.textMuted,
                      fontWeight: isActive ? FontWeight.w700 : FontWeight.w500,
                    ),
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
