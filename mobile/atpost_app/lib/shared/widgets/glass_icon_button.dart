import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:flutter/material.dart';
import 'package:flutter_animate/flutter_animate.dart';

class GlassIconButton extends StatelessWidget {
  const GlassIconButton({
    super.key,
    required this.icon,
    required this.tooltip,
    this.onPressed,
    this.size = 38,
    this.semanticLabel,
    this.iconColor,
    this.tintColor,
  });

  final IconData icon;
  final String tooltip;
  final VoidCallback? onPressed;
  final double size;
  final String? semanticLabel;

  /// Colour of the glyph. Defaults to white when null.
  final Color? iconColor;

  /// When set, renders a soft tint-coloured background instead of the
  /// neutral glass card. The glyph also picks this colour up by default
  /// if [iconColor] is not provided.
  final Color? tintColor;

  @override
  Widget build(BuildContext context) {
    final hasTint = tintColor != null;
    final bg = hasTint ? tintColor!.withValues(alpha: 0.18) : AppColors.bgCard;
    final border = hasTint ? tintColor!.withValues(alpha: 0.32) : AppColors.borderSubtle;
    final glyph = iconColor ?? (hasTint ? tintColor! : Colors.white);

    return Semantics(
      button: true,
      label: semanticLabel ?? tooltip,
      child: Tooltip(
        message: tooltip,
        child: GestureDetector(
          onTap: onPressed,
          child: Container(
                width: size,
                height: size,
                decoration: BoxDecoration(
                  color: bg,
                  borderRadius: BorderRadius.circular(99),
                  border: Border.all(color: border),
                  boxShadow: [
                    BoxShadow(
                      color: hasTint
                          ? tintColor!.withValues(alpha: 0.22)
                          : Colors.black.withValues(alpha: 0.1),
                      blurRadius: hasTint ? 10 : 4,
                      offset: const Offset(0, 2),
                    ),
                  ],
                ),
                child: Icon(icon, color: glyph, size: 20),
              )
              .animate()
              .scale(
                duration: 120.ms,
                curve: Curves.easeOut,
                begin: const Offset(1, 1),
                end: const Offset(1.02, 1.02),
              ),
        ),
      ),
    );
  }
}
