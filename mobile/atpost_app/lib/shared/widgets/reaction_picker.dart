import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:flutter/material.dart';
import 'package:flutter_animate/flutter_animate.dart';

/// Backend-supported reaction types. The order here is the same order
/// the picker renders left-to-right and matches the recon doc:
/// `like`, `love`, `wow`, `haha`, `sad`, `angry`, `spark`, `supernova`.
///
/// `spark` and `supernova` are AtPost-specific reactions
/// (`spark` -> "this is fire", `supernova` -> "this is once-in-a-lifetime
/// great"); they don't have stable Unicode emoji counterparts so we render
/// Material icons via [reactionMaterialIcon] for the picker buttons. The
/// other six reactions still render as Unicode emoji to keep parity with
/// the existing post-service serialization.
enum ReactionType {
  like,
  love,
  wow,
  haha,
  sad,
  angry,
  spark,
  supernova,
}

/// Wire identifier the post-service expects on POST /v1/posts/:id/react.
String reactionWireId(ReactionType r) {
  switch (r) {
    case ReactionType.like:
      return 'like';
    case ReactionType.love:
      return 'love';
    case ReactionType.wow:
      return 'wow';
    case ReactionType.haha:
      return 'haha';
    case ReactionType.sad:
      return 'sad';
    case ReactionType.angry:
      return 'angry';
    case ReactionType.spark:
      return 'spark';
    case ReactionType.supernova:
      return 'supernova';
  }
}

/// Reverse mapping for code paths that have the wire string and need an
/// enum (e.g. hydrating `viewer_reaction` from the API into the picker).
ReactionType? reactionTypeFromWire(String? wire) {
  if (wire == null) return null;
  switch (wire.trim().toLowerCase()) {
    case 'like':
      return ReactionType.like;
    case 'love':
      return ReactionType.love;
    case 'wow':
      return ReactionType.wow;
    case 'haha':
      return ReactionType.haha;
    case 'sad':
      return ReactionType.sad;
    case 'angry':
      return ReactionType.angry;
    case 'spark':
      return ReactionType.spark;
    case 'supernova':
      return ReactionType.supernova;
  }
  return null;
}

/// Display label for a11y / tooltips.
String reactionLabel(ReactionType r) {
  switch (r) {
    case ReactionType.like:
      return 'Like';
    case ReactionType.love:
      return 'Love';
    case ReactionType.wow:
      return 'Wow';
    case ReactionType.haha:
      return 'Haha';
    case ReactionType.sad:
      return 'Sad';
    case ReactionType.angry:
      return 'Angry';
    case ReactionType.spark:
      return 'Spark';
    case ReactionType.supernova:
      return 'Supernova';
  }
}

/// Unicode emoji for the six standard reactions. `spark` and `supernova`
/// don't have stable emoji so they fall back to Material icons.
String? reactionEmoji(ReactionType r) {
  switch (r) {
    case ReactionType.like:
      return '\u{1F44D}';
    case ReactionType.love:
      return '❤️';
    case ReactionType.haha:
      return '\u{1F602}';
    case ReactionType.wow:
      return '\u{1F62E}';
    case ReactionType.sad:
      return '\u{1F622}';
    case ReactionType.angry:
      return '\u{1F620}';
    case ReactionType.spark:
    case ReactionType.supernova:
      return null;
  }
}

/// Material icon paired with each reaction. For the six standard ones
/// the picker prefers the emoji (denser visual); the icon is the
/// fallback for places that prefer monochrome (post action row, etc.).
IconData reactionMaterialIcon(ReactionType r) {
  switch (r) {
    case ReactionType.like:
      return Icons.thumb_up_alt_outlined;
    case ReactionType.love:
      return Icons.favorite_outline;
    case ReactionType.wow:
      return Icons.sentiment_very_satisfied_outlined;
    case ReactionType.haha:
      return Icons.emoji_emotions_outlined;
    case ReactionType.sad:
      return Icons.sentiment_dissatisfied_outlined;
    case ReactionType.angry:
      return Icons.sentiment_very_dissatisfied_outlined;
    case ReactionType.spark:
      // `local_fire_department` reads as "this is fire" -- the spark
      // semantic. Outlined here because the picker is not "selected".
      return Icons.local_fire_department_outlined;
    case ReactionType.supernova:
      // `auto_awesome` is the four-pointed sparkle, which reads as
      // "out-of-this-world good" -- the supernova semantic.
      return Icons.auto_awesome_outlined;
  }
}

/// Filled-icon variant for "user has selected this reaction" state.
IconData reactionMaterialIconFilled(ReactionType r) {
  switch (r) {
    case ReactionType.like:
      return Icons.thumb_up_alt;
    case ReactionType.love:
      return Icons.favorite;
    case ReactionType.wow:
      return Icons.sentiment_very_satisfied;
    case ReactionType.haha:
      return Icons.emoji_emotions;
    case ReactionType.sad:
      return Icons.sentiment_dissatisfied;
    case ReactionType.angry:
      return Icons.sentiment_very_dissatisfied;
    case ReactionType.spark:
      return Icons.local_fire_department;
    case ReactionType.supernova:
      return Icons.auto_awesome;
  }
}

/// The eight backend-supported reactions in display order.
const List<ReactionType> reactionOrder = <ReactionType>[
  ReactionType.like,
  ReactionType.love,
  ReactionType.wow,
  ReactionType.haha,
  ReactionType.sad,
  ReactionType.angry,
  ReactionType.spark,
  ReactionType.supernova,
];

class ReactionPicker extends StatelessWidget {
  const ReactionPicker({
    super.key,
    required this.onReactionSelected,
    this.selected,
  });

  /// Callback fires with the wire identifier (e.g. "spark"). Existing
  /// callers that rely on the legacy emoji-only callback should switch
  /// to [onReactionSelected] which now hands over the post-service
  /// reaction id directly.
  final ValueChanged<String> onReactionSelected;

  /// Optional currently-selected reaction so the picker can render the
  /// filled icon variant for that entry.
  final ReactionType? selected;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 6),
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
        children: reactionOrder.asMap().entries.map((entry) {
          final index = entry.key;
          final reaction = entry.value;
          final emoji = reactionEmoji(reaction);
          final isSelected = selected == reaction;
          return Semantics(
            button: true,
            label: reactionLabel(reaction),
            selected: isSelected,
            child: GestureDetector(
              onTap: () => onReactionSelected(reactionWireId(reaction)),
              child: Padding(
                padding: const EdgeInsets.symmetric(horizontal: 5),
                child: ExcludeSemantics(
                  child: emoji != null
                      ? Text(
                          emoji,
                          style: const TextStyle(fontSize: 26),
                        )
                      : Icon(
                          isSelected
                              ? reactionMaterialIconFilled(reaction)
                              : reactionMaterialIcon(reaction),
                          size: 26,
                          // Spark/supernova get a warm tint so they read
                          // as "premium" reactions in the picker row.
                          color: reaction == ReactionType.spark
                              ? Colors.orangeAccent
                              : Colors.amberAccent,
                        ),
                ),
              ),
            ),
          )
              .animate()
              .scale(
                delay: (index * 35).ms,
                duration: 200.ms,
                curve: Curves.easeOutBack,
                begin: const Offset(0, 0),
                end: const Offset(1, 1),
              )
              .moveY(
                delay: (index * 35).ms,
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
///
/// `onSelected` receives the reaction wire id (e.g. `"spark"`).
/// Existing call sites that previously received an emoji and looked it
/// up via `_reactionTypeFor` continue to work because that helper now
/// also accepts the wire ids.
void showReactionPicker(
  BuildContext context,
  Offset position,
  void Function(String reactionId) onSelected, {
  ReactionType? selected,
}) {
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
          // Picker is wider with eight icons; nudge the anchor left so
          // the centre still lands roughly under the originating tap.
          left: position.dx - 80,
          top: position.dy - 70,
          child: Material(
            color: Colors.transparent,
            child: ReactionPicker(
              selected: selected,
              onReactionSelected: (reactionId) {
                onSelected(reactionId);
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
