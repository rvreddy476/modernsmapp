import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:flutter/material.dart';
import 'package:flutter_animate/flutter_animate.dart';

class ReactionPicker extends StatelessWidget {
  const ReactionPicker({
    super.key,
    required this.onReactionSelected,
  });

  final Function(String emoji) onReactionSelected;

  static const List<String> _emojis = [
    '❤️', '🔥', '😂', '😮', '😢', '👏', '🙌', '💯'
  ];

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 6),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(30),
        boxShadow: [
          BoxShadow(
            color: Colors.black.withValues(alpha: 0.15),
            blurRadius: 20,
            offset: const Offset(0, 10),
          ),
        ],
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: _emojis.asMap().entries.map((entry) {
          final index = entry.key;
          final emoji = entry.value;
          return GestureDetector(
            onTap: () => onReactionSelected(emoji),
            child: Padding(
              padding: const EdgeInsets.symmetric(horizontal: 6),
              child: Text(
                emoji,
                style: const TextStyle(fontSize: 26),
              ),
            ),
          )
          .animate()
          .scale(
            delay: (index * 40).ms,
            duration: 200.ms,
            curve: Curves.easeOutBack,
            begin: const Offset(0, 0),
            end: const Offset(1, 1),
          )
          .moveY(
            delay: (index * 40).ms,
            duration: 200.ms,
            begin: 20,
            end: 0,
          );
        }).toList(),
      ),
    );
  }
}

/// Helper function to show the reaction picker overlay.
void showReactionPicker(BuildContext context, Offset position, Function(String emoji) onSelected) {
  final overlay = Overlay.of(context);
  late OverlayEntry entry;

  entry = OverlayEntry(
    builder: (context) => Stack(
      children: [
        GestureDetector(
          onTap: () => entry.remove(),
          behavior: HitTestBehavior.translucent,
          child: Container(color: Colors.transparent),
        ),
        Positioned(
          left: position.dx - 40,
          top: position.dy - 70,
          child: Material(
            color: Colors.transparent,
            child: ReactionPicker(
              onReactionSelected: (emoji) {
                onSelected(emoji);
                entry.remove();
              },
            ),
          ),
        ),
      ],
    ),
  );

  overlay.insert(entry);
}
