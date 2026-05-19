import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:flutter/material.dart';

/// Shared building blocks for the Friends module surfaces (home screen +
/// three bottom sheets). Keeps avatar rendering, initials and the small
/// reusable chrome consistent across all four files.

/// Circular avatar — shows a network image when available, otherwise a
/// deterministic gradient with the name's initials.
class FriendAvatar extends StatelessWidget {
  const FriendAvatar({super.key, required this.name, this.url, this.size = 40});

  final String name;
  final String? url;
  final double size;

  @override
  Widget build(BuildContext context) {
    final colors = avatarColors(name);
    return Container(
      width: size,
      height: size,
      decoration: BoxDecoration(
        shape: BoxShape.circle,
        gradient: LinearGradient(
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
          colors: colors,
        ),
        image: (url != null && url!.isNotEmpty)
            ? DecorationImage(image: NetworkImage(url!), fit: BoxFit.cover)
            : null,
      ),
      alignment: Alignment.center,
      child: (url != null && url!.isNotEmpty)
          ? null
          : Text(
              initials(name),
              style: TextStyle(
                color: Colors.white,
                fontSize: size * 0.36,
                fontWeight: FontWeight.w600,
              ),
            ),
    );
  }
}

/// The drag handle shown at the top of every Friends-module bottom sheet.
class SheetGrabber extends StatelessWidget {
  const SheetGrabber({super.key});

  @override
  Widget build(BuildContext context) {
    return Container(
      width: 38,
      height: 4,
      margin: const EdgeInsets.only(top: 10, bottom: 4),
      decoration: BoxDecoration(
        color: AppColors.borderMedium,
        borderRadius: BorderRadius.circular(2),
      ),
    );
  }
}

/// A circular icon button with the app's subtle card chrome.
class CircleIconButton extends StatelessWidget {
  const CircleIconButton({
    super.key,
    required this.icon,
    required this.onTap,
    this.size = 18,
    this.diameter = 38,
  });

  final IconData icon;
  final VoidCallback onTap;
  final double size;
  final double diameter;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: Container(
        width: diameter,
        height: diameter,
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          shape: BoxShape.circle,
          border: Border.all(color: AppColors.borderSubtle),
        ),
        alignment: Alignment.center,
        child: Icon(icon, size: size, color: AppColors.textSecondary),
      ),
    );
  }
}

/// A tiny uppercase eyebrow label used for section headers.
class EyebrowLabel extends StatelessWidget {
  const EyebrowLabel(this.text, {super.key, this.color});

  final String text;
  final Color? color;

  @override
  Widget build(BuildContext context) {
    return Text(
      text,
      style: TextStyle(
        color: color ?? AppColors.textMuted,
        fontSize: 10,
        fontWeight: FontWeight.w700,
        letterSpacing: 0.7,
      ),
    );
  }
}

const List<List<Color>> _avatarPalette = [
  [Color(0xFF7B5BFF), Color(0xFF4A3FB0)],
  [Color(0xFF1FB6AD), Color(0xFF155E63)],
  [Color(0xFFD6249F), Color(0xFF7B2FF7)],
  [Color(0xFF5B8DEF), Color(0xFF3A4FB8)],
  [Color(0xFFFF8F65), Color(0xFFC2412A)],
  [Color(0xFF22C55E), Color(0xFF14B8A6)],
];

/// A deterministic two-colour gradient for a name's avatar fallback.
List<Color> avatarColors(String name) =>
    _avatarPalette[name.hashCode.abs() % _avatarPalette.length];

/// Up to two uppercase initials derived from a display name.
String initials(String name) {
  final parts = name
      .trim()
      .split(RegExp(r'\s+'))
      .where((p) => p.isNotEmpty)
      .toList();
  if (parts.isEmpty) return 'U';
  if (parts.length == 1) return parts.first.substring(0, 1).toUpperCase();
  return '${parts[0][0]}${parts[1][0]}'.toUpperCase();
}
