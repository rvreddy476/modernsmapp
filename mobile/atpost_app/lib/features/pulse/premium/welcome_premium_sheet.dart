// Welcome to Premium — Sprint 5.
//
// Confetti-ish celebration shown after a successful checkout. Built with
// vanilla Flutter (no `confetti` package — pubspec is locked for v1).
// Reduced motion: when `MediaQuery.disableAnimations` is true we render
// a static gradient header instead of animating the confetti dots.

import 'dart:math' as math;

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:flutter/material.dart';

class WelcomePremiumSheet {
  WelcomePremiumSheet._();

  static Future<void> show(BuildContext context) {
    return showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      backgroundColor: AppColors.bgPrimary,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(28)),
      ),
      builder: (_) => const _WelcomePremiumBody(),
    );
  }
}

class _WelcomePremiumBody extends StatefulWidget {
  const _WelcomePremiumBody();

  @override
  State<_WelcomePremiumBody> createState() => _WelcomePremiumBodyState();
}

class _WelcomePremiumBodyState extends State<_WelcomePremiumBody>
    with SingleTickerProviderStateMixin {
  late final AnimationController _ctrl;

  @override
  void initState() {
    super.initState();
    _ctrl = AnimationController(
      vsync: this,
      duration: const Duration(seconds: 2),
    )..repeat();
  }

  @override
  void dispose() {
    _ctrl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final reduceMotion = MediaQuery.disableAnimationsOf(context);
    return Padding(
      padding: EdgeInsets.fromLTRB(
        AppSpacing.xxl,
        AppSpacing.l,
        AppSpacing.xxl,
        AppSpacing.xxl + MediaQuery.viewInsetsOf(context).bottom,
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Center(
            child: Container(
              width: 36,
              height: 4,
              decoration: BoxDecoration(
                color: AppColors.borderSubtle,
                borderRadius: BorderRadius.circular(999),
              ),
            ),
          ),
          const SizedBox(height: AppSpacing.l),
          SizedBox(
            height: 120,
            child: reduceMotion
                ? const _StaticGradientHeader()
                : AnimatedBuilder(
                    animation: _ctrl,
                    builder: (_, _) => CustomPaint(
                      painter: _ConfettiPainter(t: _ctrl.value),
                      child: const Center(
                        child: Icon(
                          Icons.bolt_rounded,
                          size: 56,
                          color: Colors.white,
                        ),
                      ),
                    ),
                  ),
          ),
          const SizedBox(height: AppSpacing.l),
          Semantics(
            header: true,
            child: Text(
              'You\'re now a Premium member',
              style: AppTextStyles.h1,
              textAlign: TextAlign.center,
            ),
          ),
          const SizedBox(height: AppSpacing.xs),
          Text(
            'Here\'s what you unlocked.',
            style: AppTextStyles.bodySmall.copyWith(
              color: AppColors.textTertiary,
            ),
            textAlign: TextAlign.center,
          ),
          const SizedBox(height: AppSpacing.xxl),
          ..._kPremiumFeatures.map(
            (f) => Padding(
              padding: const EdgeInsets.symmetric(vertical: 6),
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  const Icon(
                    Icons.check_circle_rounded,
                    color: AppColors.postbookPrimary,
                    size: 20,
                  ),
                  const SizedBox(width: 12),
                  Expanded(
                    child: Text(f, style: AppTextStyles.body),
                  ),
                ],
              ),
            ),
          ),
          const SizedBox(height: AppSpacing.xxl),
          ElevatedButton(
            style: ElevatedButton.styleFrom(
              backgroundColor: AppColors.postbookPrimary,
              padding: const EdgeInsets.symmetric(vertical: 14),
            ),
            onPressed: () => Navigator.of(context).pop(),
            child: const Text('Start exploring'),
          ),
        ],
      ),
    );
  }
}

const List<String> _kPremiumFeatures = [
  'Unlimited Sparks',
  'See who Sparked you',
  '25 Stash slots',
  'Incognito browse',
  'Pulse Boost — +5 daily, 1 per day',
  'Match-extend (+7 days)',
  'Advanced Tune filters',
  'Safe-meet check-in',
  'Priority moderation review',
  'Read receipts (per match)',
];

class _StaticGradientHeader extends StatelessWidget {
  const _StaticGradientHeader();

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        borderRadius: BorderRadius.circular(20),
        gradient: const LinearGradient(
          colors: [
            Color(0xFF6E36CC),
            Color(0xFFD1419C),
            Color(0xFFFF9233),
          ],
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
        ),
      ),
      child: const Center(
        child: Icon(
          Icons.bolt_rounded,
          size: 56,
          color: Colors.white,
        ),
      ),
    );
  }
}

class _ConfettiPainter extends CustomPainter {
  _ConfettiPainter({required this.t});
  final double t;

  static const _palette = [
    Color(0xFF6E36CC),
    Color(0xFFD1419C),
    Color(0xFFFF9233),
    Color(0xFF38C172),
    Color(0xFFE9C341),
  ];

  @override
  void paint(Canvas canvas, Size size) {
    // Static gradient backplate.
    final rect = Offset.zero & size;
    final bg = Paint()
      ..shader = const LinearGradient(
        colors: [
          Color(0xFF2A1652),
          Color(0xFF6E36CC),
          Color(0xFFD1419C),
        ],
        begin: Alignment.topLeft,
        end: Alignment.bottomRight,
      ).createShader(rect);
    canvas.drawRRect(
      RRect.fromRectAndRadius(rect, const Radius.circular(20)),
      bg,
    );

    // Slow falling confetti — 32 dots looping over `t`.
    final rand = math.Random(7);
    for (var i = 0; i < 32; i++) {
      final x = rand.nextDouble() * size.width;
      final speed = 0.4 + rand.nextDouble() * 0.8;
      final phase = rand.nextDouble();
      final y = ((t * speed) + phase) % 1.0 * size.height;
      final r = 3.0 + rand.nextDouble() * 3.0;
      final c = _palette[i % _palette.length];
      canvas.drawCircle(
        Offset(x, y),
        r,
        Paint()..color = c.withValues(alpha: 0.85),
      );
    }
  }

  @override
  bool shouldRepaint(covariant _ConfettiPainter old) => old.t != t;
}
