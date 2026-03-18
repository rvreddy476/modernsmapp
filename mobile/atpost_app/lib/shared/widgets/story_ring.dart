import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:flutter/material.dart';
import 'package:flutter_animate/flutter_animate.dart';

class StoryRing extends StatelessWidget {
  const StoryRing({
    super.key,
    required this.initials,
    required this.label,
    this.isLive = false,
    this.isOwn = false,
  });

  final String initials;
  final String label;
  final bool isLive;
  final bool isOwn;

  String get _semanticLabel {
    if (isOwn) return 'Add your story';
    if (isLive) return '$label, live';
    return '$label story';
  }

  @override
  Widget build(BuildContext context) {
    final border = isOwn
        ? const [Color(0x22FFFFFF), Color(0x22FFFFFF)]
        : AppColors.storyRingGradient.colors;
    return Semantics(
      button: true,
      label: _semanticLabel,
      child: Column(
        children: [
          Stack(
            clipBehavior: Clip.none,
            children: [
              Container(
                width: 64,
                height: 64,
                padding: const EdgeInsets.all(3),
                decoration: BoxDecoration(
                  shape: BoxShape.circle,
                  gradient: LinearGradient(colors: border),
                  boxShadow: isLive
                      ? [
                          const BoxShadow(
                            color: Color(0x66FF3366),
                            blurRadius: 14,
                            spreadRadius: 2,
                          ),
                        ]
                      : const [],
                ),
                child: Container(
                  decoration: const BoxDecoration(
                    shape: BoxShape.circle,
                    color: AppColors.bgTertiary,
                  ),
                  child: Center(
                    child: ExcludeSemantics(
                      child: Text(
                        initials,
                        style: AppTextStyles.h3,
                      ),
                    ),
                  ),
                ),
              ),
              if (isOwn)
                Positioned(
                  right: -2,
                  bottom: -2,
                  child: ExcludeSemantics(
                    child: Container(
                      width: 20,
                      height: 20,
                      decoration: BoxDecoration(
                        gradient: AppColors.ctaGradient,
                        shape: BoxShape.circle,
                        border: Border.all(color: AppColors.bgPrimary, width: 2),
                      ),
                      child: const Icon(Icons.add, size: 12, color: Colors.white),
                    ),
                  ),
                ),
              if (isLive)
                Positioned(
                  left: 11,
                  bottom: -8,
                  child: ExcludeSemantics(
                    child: Container(
                      padding:
                          const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                      decoration: BoxDecoration(
                        color: AppColors.liveRed,
                        borderRadius: BorderRadius.circular(999),
                        border: Border.all(color: AppColors.bgPrimary, width: 2),
                      ),
                      child: Text(
                        'LIVE',
                        style: AppTextStyles.labelTiny.copyWith(color: Colors.white),
                      ),
                    )
                        .animate(onPlay: (controller) => controller.repeat())
                        .fade(begin: 0.6, end: 1, duration: 900.ms),
                  ),
                ),
            ],
          ),
          const SizedBox(height: 6),
          ExcludeSemantics(
            child: SizedBox(
              width: 70,
              child: Text(
                label,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                textAlign: TextAlign.center,
                style: AppTextStyles.labelSmall,
              ),
            ),
          ),
        ],
      ),
    );
  }
}
