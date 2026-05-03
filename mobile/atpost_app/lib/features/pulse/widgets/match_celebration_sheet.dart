import 'dart:math' as math;

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/pulse.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:go_router/go_router.dart';

/// Sprint 3 — Match celebration sheet.
///
/// Shown when a Spark mutually matches. Big "It's a Match!" headline with a
/// subtle nebula motion, two avatars side-by-side with a soft pulse between
/// them, and a one-liner tying the celebration back to the spark's target.
///
/// Two CTAs:
///   - "Say hi" (primary): opens the conversation if available.
///   - "Keep exploring" (dismiss).
///
/// Reduced motion: replace the rotating nebula with a static gradient and
/// freeze the heartbeat tween. Spec §6.4 / §7.
class MatchCelebrationSheet {
  MatchCelebrationSheet._();

  static Future<void> show(
    BuildContext context, {
    required MatchDetail match,
    required SparkContext sparkContext,
  }) {
    return showModalBottomSheet<void>(
      context: context,
      backgroundColor: Colors.transparent,
      isScrollControlled: true,
      barrierColor: Colors.black.withValues(alpha: 0.7),
      builder: (ctx) => _MatchCelebrationSheet(
        match: match,
        sparkContext: sparkContext,
      ),
    );
  }
}

class _MatchCelebrationSheet extends StatefulWidget {
  const _MatchCelebrationSheet({
    required this.match,
    required this.sparkContext,
  });

  final MatchDetail match;
  final SparkContext sparkContext;

  @override
  State<_MatchCelebrationSheet> createState() => _MatchCelebrationSheetState();
}

class _MatchCelebrationSheetState extends State<_MatchCelebrationSheet>
    with TickerProviderStateMixin {
  late final AnimationController _nebula;
  late final AnimationController _heartbeat;

  @override
  void initState() {
    super.initState();
    _nebula = AnimationController(
      vsync: this,
      duration: const Duration(seconds: 14),
    )..repeat();
    _heartbeat = AnimationController(
      vsync: this,
      duration: const Duration(milliseconds: 1400),
    )..repeat(reverse: true);
    HapticFeedback.mediumImpact();
  }

  @override
  void dispose() {
    _nebula.dispose();
    _heartbeat.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final reducedMotion = MediaQuery.of(context).disableAnimations;
    if (reducedMotion) {
      _nebula.stop();
      _heartbeat.stop();
    }
    return DraggableScrollableSheet(
      initialChildSize: 0.92,
      minChildSize: 0.5,
      maxChildSize: 0.97,
      builder: (context, controller) {
        return ClipRRect(
          borderRadius: const BorderRadius.vertical(
            top: Radius.circular(28),
          ),
          child: Stack(
            children: [
              Positioned.fill(
                child: _NebulaBackground(
                  controller: _nebula,
                  reducedMotion: reducedMotion,
                ),
              ),
              SingleChildScrollView(
                controller: controller,
                padding: const EdgeInsets.fromLTRB(20, 20, 20, 32),
                child: Column(
                  children: [
                    Center(
                      child: Container(
                        width: 38,
                        height: 4,
                        decoration: BoxDecoration(
                          color: Colors.white24,
                          borderRadius: BorderRadius.circular(2),
                        ),
                      ),
                    ),
                    const SizedBox(height: 30),
                    ShaderMask(
                      shaderCallback: (rect) =>
                          AppColors.ctaGradient.createShader(rect),
                      child: Text(
                        "It's a Match!",
                        style: AppTextStyles.h1.copyWith(
                          fontSize: 44,
                          color: Colors.white,
                          fontWeight: FontWeight.w800,
                        ),
                        textAlign: TextAlign.center,
                      ),
                    ),
                    const SizedBox(height: 12),
                    Text(
                      'You both Sparked the same thing.',
                      style: AppTextStyles.body.copyWith(
                        color: Colors.white70,
                      ),
                      textAlign: TextAlign.center,
                    ),
                    const SizedBox(height: 32),
                    SizedBox(
                      height: 160,
                      child: _Avatars(
                        otherFirstName: widget.match.otherFirstName,
                        otherAvatarUrl: widget.match.otherAvatarUrl,
                        controller: _heartbeat,
                        reducedMotion: reducedMotion,
                      ),
                    ),
                    const SizedBox(height: 24),
                    _SparkContextCard(context: widget.sparkContext),
                    const SizedBox(height: 28),
                    _PrimaryCta(
                      label: 'Say hi to ${widget.match.otherFirstName}',
                      onTap: () {
                        Navigator.of(context).pop();
                        final convo = widget.match.conversationId;
                        if (convo == null || convo.isEmpty) {
                          ScaffoldMessenger.of(context).showSnackBar(
                            const SnackBar(
                              content: Text('Conversation not ready yet.'),
                            ),
                          );
                          return;
                        }
                        // Use a microtask so the sheet pop can settle.
                        Future.microtask(() {
                          if (!context.mounted) return;
                          context.push('/pulse/chat/$convo');
                        });
                      },
                    ),
                    const SizedBox(height: 12),
                    TextButton(
                      onPressed: () => Navigator.of(context).pop(),
                      child: Text(
                        'Keep exploring',
                        style: AppTextStyles.label.copyWith(
                          color: Colors.white70,
                        ),
                      ),
                    ),
                  ],
                ),
              ),
            ],
          ),
        );
      },
    );
  }
}

class _NebulaBackground extends StatelessWidget {
  const _NebulaBackground({
    required this.controller,
    required this.reducedMotion,
  });

  final AnimationController controller;
  final bool reducedMotion;

  @override
  Widget build(BuildContext context) {
    if (reducedMotion) {
      return const DecoratedBox(
        decoration: BoxDecoration(
          gradient: LinearGradient(
            begin: Alignment.topLeft,
            end: Alignment.bottomRight,
            colors: [
              Color(0xFF1A0F2A),
              Color(0xFF0D0D18),
            ],
          ),
        ),
      );
    }
    return AnimatedBuilder(
      animation: controller,
      builder: (context, _) {
        final t = controller.value;
        // Slowly rotating sweep gradient gives a "nebula" feel without
        // shipping a video / lottie.
        return DecoratedBox(
          decoration: BoxDecoration(
            gradient: SweepGradient(
              center: Alignment.center,
              startAngle: 0,
              endAngle: 2 * math.pi,
              transform: GradientRotation(2 * math.pi * t),
              colors: const [
                Color(0xFF1A0F2A),
                Color(0xFF301244),
                Color(0xFF111025),
                Color(0xFF1A0F2A),
              ],
            ),
          ),
        );
      },
    );
  }
}

class _Avatars extends StatelessWidget {
  const _Avatars({
    required this.otherFirstName,
    required this.otherAvatarUrl,
    required this.controller,
    required this.reducedMotion,
  });

  final String otherFirstName;
  final String? otherAvatarUrl;
  final AnimationController controller;
  final bool reducedMotion;

  @override
  Widget build(BuildContext context) {
    return AnimatedBuilder(
      animation: controller,
      builder: (context, _) {
        final t = reducedMotion ? 0.5 : controller.value;
        final scale = 1.0 + 0.05 * math.sin(t * math.pi);
        return Stack(
          alignment: Alignment.center,
          children: [
            // Soft pulse halo between the avatars.
            Container(
              width: 110 * scale,
              height: 110 * scale,
              decoration: BoxDecoration(
                shape: BoxShape.circle,
                gradient: AppColors.ctaGradient,
                boxShadow: [
                  BoxShadow(
                    color: AppColors.postgramPrimary.withValues(alpha: 0.5),
                    blurRadius: 40,
                    spreadRadius: 8,
                  ),
                ],
              ),
            ),
            // Two avatars overlapping the halo.
            Row(
              mainAxisAlignment: MainAxisAlignment.center,
              children: [
                _AvatarCircle(
                  imageUrl: null,
                  fallback: 'You',
                  shift: -22.0,
                ),
                _AvatarCircle(
                  imageUrl: otherAvatarUrl,
                  fallback: otherFirstName.isNotEmpty
                      ? otherFirstName.substring(0, 1)
                      : '?',
                  shift: 22.0,
                ),
              ],
            ),
          ],
        );
      },
    );
  }
}

class _AvatarCircle extends StatelessWidget {
  const _AvatarCircle({
    required this.imageUrl,
    required this.fallback,
    required this.shift,
  });

  final String? imageUrl;
  final String fallback;
  final double shift;

  @override
  Widget build(BuildContext context) {
    return Transform.translate(
      offset: Offset(shift, 0),
      child: Container(
        width: 100,
        height: 100,
        decoration: BoxDecoration(
          shape: BoxShape.circle,
          color: AppColors.bgSecondary,
          border: Border.all(color: Colors.white, width: 3),
        ),
        child: ClipOval(
          child: imageUrl != null && imageUrl!.isNotEmpty
              ? Image.network(
                  imageUrl!,
                  fit: BoxFit.cover,
                  errorBuilder: (_, _, _) =>
                      const ColoredBox(color: AppColors.bgTertiary),
                )
              : Container(
                  color: AppColors.bgTertiary,
                  alignment: Alignment.center,
                  child: Text(fallback, style: AppTextStyles.h2),
                ),
        ),
      ),
    );
  }
}

class _SparkContextCard extends StatelessWidget {
  const _SparkContextCard({required this.context});

  final SparkContext context;

  @override
  Widget build(BuildContext ctx) {
    final summary = context.summary.isNotEmpty
        ? context.summary
        : context.targetRef;
    final kindLabel = _kindLabel(context.targetKind);
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: Colors.white.withValues(alpha: 0.10),
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: Colors.white24),
      ),
      child: Row(
        children: [
          Icon(_kindIcon(context.targetKind), color: Colors.white, size: 22),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  'You both Sparked the $kindLabel',
                  style: AppTextStyles.labelSmall.copyWith(
                    color: Colors.white70,
                  ),
                ),
                const SizedBox(height: 4),
                Text(
                  summary,
                  style: AppTextStyles.label.copyWith(color: Colors.white),
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

String _kindLabel(SparkTargetKind kind) {
  switch (kind) {
    case SparkTargetKind.photo:
      return 'photo';
    case SparkTargetKind.prompt:
      return 'prompt';
    case SparkTargetKind.tuneAxis:
      return 'tune';
    case SparkTargetKind.echoQa:
      return 'Q&A answer';
    case SparkTargetKind.echoReel:
      return 'reel';
    case SparkTargetKind.echoCommunity:
      return 'community';
    case SparkTargetKind.echoPost:
      return 'post';
  }
}

IconData _kindIcon(SparkTargetKind kind) {
  switch (kind) {
    case SparkTargetKind.photo:
      return Icons.photo_outlined;
    case SparkTargetKind.prompt:
      return Icons.format_quote_outlined;
    case SparkTargetKind.tuneAxis:
      return Icons.tune;
    case SparkTargetKind.echoQa:
      return Icons.question_answer_outlined;
    case SparkTargetKind.echoReel:
      return Icons.play_circle_outline;
    case SparkTargetKind.echoCommunity:
      return Icons.groups_outlined;
    case SparkTargetKind.echoPost:
      return Icons.article_outlined;
  }
}

class _PrimaryCta extends StatelessWidget {
  const _PrimaryCta({required this.label, required this.onTap});

  final String label;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: double.infinity,
      height: 52,
      child: ElevatedButton.icon(
        onPressed: onTap,
        style: ElevatedButton.styleFrom(
          backgroundColor: Colors.white,
          foregroundColor: AppColors.bgPrimary,
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
          ),
        ),
        icon: const Icon(Icons.waving_hand_outlined),
        label: Text(
          label,
          style: AppTextStyles.label.copyWith(
            color: AppColors.bgPrimary,
            fontWeight: FontWeight.w700,
          ),
        ),
      ),
    );
  }
}
