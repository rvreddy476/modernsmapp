import 'dart:math' as math;

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/pulse.dart';
import 'package:atpost_app/providers/user_provider.dart';
import 'package:atpost_app/services/pulse_crash_breadcrumbs.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Sprint 2 — Orbital canvas.
///
/// The hero surface for `PulseDiscoverScreen`. Renders the viewer in the
/// middle and 3–5 candidates on a slow elliptical orbit. Drag a candidate
/// inward to peel back match-reasons; release at center to spark; flick
/// outward to pass; drag to right edge to stash.
///
/// Reduced motion: when `MediaQuery.disableAnimations` is true the orbit
/// freezes and candidates render as a static grid of cards.
///
/// This is intentionally vanilla Flutter — `AnimationController` + `Tween` +
/// `CustomPainter`, no `flutter_animate` / `rive`. Polish lives in S6.
class OrbitalCanvas extends ConsumerStatefulWidget {
  const OrbitalCanvas({
    super.key,
    required this.cards,
    required this.onCardOpen,
    required this.onSpark,
    required this.onStash,
    required this.onPass,
  });

  final List<PulseCard> cards;
  final ValueChanged<PulseCard> onCardOpen;
  final ValueChanged<PulseCard> onSpark;
  final ValueChanged<PulseCard> onStash;
  final ValueChanged<PulseCard> onPass;

  @override
  ConsumerState<OrbitalCanvas> createState() => _OrbitalCanvasState();
}

class _OrbitalCanvasState extends ConsumerState<OrbitalCanvas>
    with TickerProviderStateMixin {
  /// Slow orbit — one full revolution every 30s (calming, not a slot machine).
  static const Duration _orbitPeriod = Duration(seconds: 30);

  /// Max number of candidates kept in the active orbit. Anything beyond this
  /// becomes an off-screen "nebula" dot.
  static const int _maxOrbitalNodes = 5;

  /// Velocity threshold (px/s) for a "pass" flick.
  static const double _passVelocityThreshold = 600;

  /// Hold duration to lock the inner-layer expansion.
  static const Duration _unfoldHold = Duration(milliseconds: 300);

  late final AnimationController _orbitController;

  /// Pinch-zoom (1.0 = focus, < 1.0 = nebula view).
  double _zoom = 1.0;
  double _zoomStart = 1.0;

  /// The currently-dragged candidate, if any.
  String? _draggedId;
  Offset _dragOffset = Offset.zero;
  /// 0 = on the rail, 1 = at center.
  double _dragPullIn = 0;
  bool _stashCrossed = false;
  bool _innerLocked = false;
  DateTime? _holdAnchor;

  @override
  void initState() {
    super.initState();
    _orbitController = AnimationController(
      vsync: this,
      duration: _orbitPeriod,
    )..repeat();
  }

  @override
  void dispose() {
    _orbitController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final reducedMotion = MediaQuery.of(context).disableAnimations;
    if (reducedMotion) {
      _orbitController.stop();
      return _ReducedMotionGrid(
        cards: widget.cards,
        onCardOpen: widget.onCardOpen,
        onSpark: widget.onSpark,
        onStash: widget.onStash,
        onPass: widget.onPass,
      );
    } else if (!_orbitController.isAnimating) {
      _orbitController.repeat();
    }

    final orbitalCards = widget.cards.take(_maxOrbitalNodes).toList();
    final nebulaCards = widget.cards.length > _maxOrbitalNodes
        ? widget.cards.sublist(_maxOrbitalNodes)
        : const <PulseCard>[];

    return LayoutBuilder(
      builder: (context, constraints) {
        final size = Size(constraints.maxWidth, constraints.maxHeight);
        final center = Offset(size.width / 2, size.height / 2);
        // Scale the orbit to fit the smaller side of the canvas, leaving
        // padding for nebula dots and the stash shelf.
        final orbitR = math.min(size.width, size.height) * 0.34 * _zoom;
        final orbitRy = orbitR * 0.78; // gentle ellipse

        return GestureDetector(
          behavior: HitTestBehavior.opaque,
          onScaleStart: (details) => _zoomStart = _zoom,
          onScaleUpdate: (details) {
            // Only react to pinch (2+ pointers). For single pointer drags,
            // child gesture detectors handle node motion.
            if (details.pointerCount >= 2) {
              setState(() {
                _zoom = (_zoomStart * details.scale).clamp(0.55, 1.25);
              });
            }
          },
          child: Stack(
            children: [
              // Painted background: orbit ellipse + breathing rings.
              Positioned.fill(
                child: AnimatedBuilder(
                  animation: _orbitController,
                  builder: (context, _) {
                    return CustomPaint(
                      painter: _OrbitPainter(
                        center: center,
                        rx: orbitR,
                        ry: orbitRy,
                        breath: _orbitController.value,
                      ),
                    );
                  },
                ),
              ),

              // Stash shelf glow on the right edge.
              if (_draggedId != null)
                Positioned(
                  right: 0,
                  top: 0,
                  bottom: 0,
                  width: 28,
                  child: IgnorePointer(
                    child: AnimatedOpacity(
                      duration: const Duration(milliseconds: 150),
                      opacity: _stashCrossed ? 1.0 : 0.35,
                      child: Container(
                        decoration: BoxDecoration(
                          gradient: LinearGradient(
                            begin: Alignment.centerLeft,
                            end: Alignment.centerRight,
                            colors: [
                              AppColors.accentPurple.withValues(alpha: 0),
                              AppColors.accentPurple.withValues(alpha: 0.45),
                            ],
                          ),
                        ),
                        child: const Center(
                          child: Icon(
                            Icons.bookmark_outline,
                            color: Colors.white,
                          ),
                        ),
                      ),
                    ),
                  ),
                ),

              // Center: viewer's avatar with a soft breathing glow.
              _CenterAvatar(
                center: center,
                onTap: () {
                  // Tapping self avatar opens self profile; spec says yes.
                  // Routing handled by parent via the existing /pulse/profile.
                  Navigator.of(context).maybePop();
                },
                pulse: _orbitController,
              ),

              // Off-orbit nebula dots — show only when zoomed out.
              if (_zoom < 0.95)
                ..._buildNebulaDots(center, orbitR, orbitRy, nebulaCards),

              // Orbital nodes (one per candidate).
              for (var i = 0; i < orbitalCards.length; i++)
                _buildCandidateNode(
                  card: orbitalCards[i],
                  index: i,
                  total: orbitalCards.length,
                  center: center,
                  rx: orbitR,
                  ry: orbitRy,
                  size: size,
                ),
            ],
          ),
        );
      },
    );
  }

  Widget _buildCandidateNode({
    required PulseCard card,
    required int index,
    required int total,
    required Offset center,
    required double rx,
    required double ry,
    required Size size,
  }) {
    return AnimatedBuilder(
      animation: _orbitController,
      builder: (context, _) {
        final isDragged = _draggedId == card.candidateId;
        // Spread candidates evenly around the ellipse, then advance with the
        // orbit controller's clock.
        final phase =
            (_orbitController.value + (index / math.max(total, 1))) *
            2 *
            math.pi;
        final orbitX = center.dx + math.cos(phase) * rx;
        final orbitY = center.dy + math.sin(phase) * ry;

        Offset position;
        double pullIn;
        if (isDragged) {
          position = _dragOffset;
          pullIn = _dragPullIn;
        } else {
          position = Offset(orbitX, orbitY);
          pullIn = 0;
        }

        // Node grows as it's pulled toward the center.
        final scale = 1.0 + pullIn * 0.6;

        // The visual layer hint:
        //  - layer 0 (outer): default
        //  - layer 1 (mid): pullIn >= 0.6
        //  - layer 2 (inner): pullIn >= 0.9 OR _innerLocked
        final layer = (_innerLocked || pullIn >= 0.9)
            ? 2
            : pullIn >= 0.6
                ? 1
                : 0;

        return Positioned(
          left: position.dx - 64,
          top: position.dy - 64,
          width: 128,
          height: 128,
          child: GestureDetector(
            behavior: HitTestBehavior.opaque,
            onTap: () => widget.onCardOpen(card),
            onPanStart: (details) {
              PulseBreadcrumbs.orbitalDragStart();
              setState(() {
                _draggedId = card.candidateId;
                _dragOffset = Offset(orbitX, orbitY);
                _dragPullIn = 0;
                _stashCrossed = false;
                _innerLocked = false;
                _holdAnchor = null;
              });
            },
            onPanUpdate: (details) {
              final newPos = _dragOffset + details.delta;
              final dx = newPos.dx - center.dx;
              final dy = newPos.dy - center.dy;
              final dist = math.sqrt(dx * dx + dy * dy);
              // Distance on orbit boundary (averaged radius) is the
              // baseline; closer than that => pull-in > 0.
              final boundary = (rx + ry) / 2;
              final newPullIn = (1.0 - (dist / boundary)).clamp(0.0, 1.0);
              final crossedRight = newPos.dx > size.width - 36;
              setState(() {
                _dragOffset = newPos;
                _dragPullIn = newPullIn;
                if (crossedRight && !_stashCrossed) {
                  _stashCrossed = true;
                  _hapticLight();
                }
                if (!crossedRight && _stashCrossed) {
                  _stashCrossed = false;
                }
                // Hold-to-unfold: track when we land near center.
                if (newPullIn >= 0.85) {
                  _holdAnchor ??= DateTime.now();
                  if (!_innerLocked &&
                      DateTime.now().difference(_holdAnchor!) >= _unfoldHold) {
                    _innerLocked = true;
                    _hapticTick();
                  }
                } else {
                  _holdAnchor = null;
                  _innerLocked = false;
                }
              });
            },
            onPanEnd: (details) {
              final velocity = details.velocity.pixelsPerSecond;
              final speed = velocity.distance;
              final dx = _dragOffset.dx - center.dx;
              final dy = _dragOffset.dy - center.dy;
              final dist = math.sqrt(dx * dx + dy * dy);

              // Capture to local before clearing state.
              final wasStashed = _stashCrossed;
              final pullIn = _dragPullIn;

              setState(() {
                _draggedId = null;
                _dragOffset = Offset.zero;
                _dragPullIn = 0;
                _stashCrossed = false;
                _innerLocked = false;
                _holdAnchor = null;
              });

              if (wasStashed) {
                PulseBreadcrumbs.orbitalDragRelease(committed: true);
                widget.onStash(card);
                return;
              }
              // Released at center -> spark.
              if (pullIn >= 0.85 || dist < (rx + ry) / 6) {
                PulseBreadcrumbs.orbitalDragRelease(committed: true);
                widget.onSpark(card);
                return;
              }
              // Outward flick -> pass.
              final outward = (dx * velocity.dx + dy * velocity.dy) > 0;
              if (outward && speed > _passVelocityThreshold) {
                PulseBreadcrumbs.orbitalDragRelease(committed: true);
                widget.onPass(card);
                return;
              }
              // Otherwise: snap back; orbit picks up where it left off.
              PulseBreadcrumbs.orbitalDragRelease(committed: false);
            },
            child: Transform.scale(
              scale: scale,
              child: _CandidateNode(card: card, layer: layer),
            ),
          ),
        );
      },
    );
  }

  List<Widget> _buildNebulaDots(
    Offset center,
    double rx,
    double ry,
    List<PulseCard> cards,
  ) {
    return [
      for (var i = 0; i < cards.length; i++)
        Positioned(
          left:
              center.dx +
              math.cos(i * 0.72) * rx * 1.65 -
              4,
          top:
              center.dy +
              math.sin(i * 0.72) * ry * 1.6 -
              4,
          width: 8,
          height: 8,
          child: GestureDetector(
            onTap: () => widget.onCardOpen(cards[i]),
            child: const DecoratedBox(
              decoration: BoxDecoration(
                color: AppColors.textTertiary,
                shape: BoxShape.circle,
              ),
            ),
          ),
        ),
    ];
  }

  void _hapticLight() {
    HapticFeedback.lightImpact();
  }

  void _hapticTick() {
    HapticFeedback.selectionClick();
  }
}

// ---------------------------------------------------------------------------
// Center avatar (viewer's own glow).
// ---------------------------------------------------------------------------

class _CenterAvatar extends ConsumerWidget {
  const _CenterAvatar({
    required this.center,
    required this.onTap,
    required this.pulse,
  });

  final Offset center;
  final VoidCallback onTap;
  final Animation<double> pulse;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final user = ref.watch(currentUserProvider);
    return Positioned(
      left: center.dx - 44,
      top: center.dy - 44,
      width: 88,
      height: 88,
      child: AnimatedBuilder(
        animation: pulse,
        builder: (context, _) {
          // Breathing scale: 0.92..1.06 every revolution.
          final t = (math.sin(pulse.value * 2 * math.pi) + 1) / 2;
          final glowScale = 0.92 + t * 0.14;
          return Transform.scale(
            scale: glowScale,
            child: GestureDetector(
              onTap: onTap,
              child: DecoratedBox(
                decoration: BoxDecoration(
                  shape: BoxShape.circle,
                  gradient: AppColors.ctaGradient,
                  boxShadow: [
                    BoxShadow(
                      color: AppColors.postbookPrimary.withValues(alpha: 0.35),
                      blurRadius: 30,
                      spreadRadius: 6,
                    ),
                  ],
                ),
                child: user.maybeWhen(
                  data: (u) => Padding(
                    padding: const EdgeInsets.all(3),
                    child: ClipOval(
                      child: u.hasAvatar
                          ? Image.network(u.avatarUrl, fit: BoxFit.cover)
                          : Container(
                              color: AppColors.bgSecondary,
                              alignment: Alignment.center,
                              child: Text(
                                u.displayName.isEmpty
                                    ? 'Y'
                                    : u.displayName.substring(0, 1),
                                style: AppTextStyles.h2,
                              ),
                            ),
                    ),
                  ),
                  orElse: () => const SizedBox.shrink(),
                ),
              ),
            ),
          );
        },
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Candidate node.
// ---------------------------------------------------------------------------

class _CandidateNode extends StatelessWidget {
  const _CandidateNode({required this.card, required this.layer});

  final PulseCard card;
  /// 0 = outer (default), 1 = mid (top reason), 2 = inner (full).
  final int layer;

  @override
  Widget build(BuildContext context) {
    final intentColor = _intentColor(card.profile.intent);
    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        Container(
          width: 56,
          height: 56,
          decoration: BoxDecoration(
            shape: BoxShape.circle,
            border: Border.all(color: intentColor, width: 2),
            color: AppColors.bgSecondary,
            boxShadow: [
              BoxShadow(
                color: intentColor.withValues(alpha: 0.25),
                blurRadius: 14,
                spreadRadius: 1,
              ),
            ],
          ),
          child: ClipOval(
            child:
                card.profile.primaryPhotoUrl != null &&
                        card.profile.primaryPhotoUrl!.isNotEmpty
                    ? Image.network(
                        card.profile.primaryPhotoUrl!,
                        fit: BoxFit.cover,
                      )
                    : Container(
                        alignment: Alignment.center,
                        child: Text(
                          card.profile.firstName.isEmpty
                              ? '?'
                              : card.profile.firstName.substring(0, 1),
                          style: AppTextStyles.h3,
                        ),
                      ),
          ),
        ),
        const SizedBox(height: 4),
        Text(
          '${card.profile.firstName}, ${card.profile.age}',
          style: AppTextStyles.labelSmall.copyWith(
            color: AppColors.textPrimary,
          ),
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
        ),
        const SizedBox(height: 2),
        Container(
          padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
          decoration: BoxDecoration(
            color: intentColor.withValues(alpha: 0.18),
            borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
          ),
          child: Text(
            card.profile.intent,
            style: AppTextStyles.labelTiny.copyWith(color: intentColor),
          ),
        ),
        if (layer >= 1 && card.matchReasons.isNotEmpty)
          Padding(
            padding: const EdgeInsets.only(top: 4),
            child: Container(
              padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
              decoration: BoxDecoration(
                color: AppColors.bgSecondary.withValues(alpha: 0.92),
                borderRadius: BorderRadius.circular(AppSpacing.radiusSmall),
                border: Border.all(color: AppColors.borderSubtle),
              ),
              child: Text(
                card.matchReasons.first.summary,
                style: AppTextStyles.labelTiny.copyWith(
                  color: AppColors.textSecondary,
                ),
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                textAlign: TextAlign.center,
              ),
            ),
          ),
        if (layer >= 2)
          Padding(
            padding: const EdgeInsets.only(top: 4),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                if (card.profile.tuneSummary?.conversationStyle != null)
                  Text(
                    'Tune: ${card.profile.tuneSummary!.conversationStyle}',
                    style: AppTextStyles.labelTiny,
                  ),
                if (card.profile.trustTier != null)
                  Padding(
                    padding: const EdgeInsets.only(top: 2),
                    child: Row(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        const Icon(
                          Icons.verified,
                          size: 10,
                          color: AppColors.statusSuccess,
                        ),
                        const SizedBox(width: 2),
                        Text(
                          card.profile.trustTier!,
                          style: AppTextStyles.labelTiny.copyWith(
                            color: AppColors.statusSuccess,
                          ),
                        ),
                      ],
                    ),
                  ),
              ],
            ),
          ),
      ],
    );
  }
}

Color _intentColor(String intent) {
  switch (intent) {
    case 'casual':
      return AppColors.statusWarning; // amber
    case 'serious':
      return AppColors.accentPurple; // violet
    case 'marriage':
      return AppColors.postgramPrimary; // rose
    default:
      return AppColors.textTertiary;
  }
}

// ---------------------------------------------------------------------------
// Custom painter — orbit ellipse + faint concentric breathing rings.
// ---------------------------------------------------------------------------

class _OrbitPainter extends CustomPainter {
  _OrbitPainter({
    required this.center,
    required this.rx,
    required this.ry,
    required this.breath,
  });

  final Offset center;
  final double rx;
  final double ry;
  final double breath;

  @override
  void paint(Canvas canvas, Size size) {
    // Soft fill for a subtle "depth" feel.
    final bg = Paint()..color = AppColors.bgPrimary;
    canvas.drawRect(Offset.zero & size, bg);

    // Concentric breathing rings (3 of them, phased).
    for (var i = 0; i < 3; i++) {
      final phase = (breath + i / 3) % 1;
      final factor = 0.6 + phase * 0.6; // 0.6..1.2
      final paint = Paint()
        ..style = PaintingStyle.stroke
        ..strokeWidth = 1
        ..color = AppColors.borderMedium.withValues(
          alpha: 0.18 * (1 - phase),
        );
      canvas.drawOval(
        Rect.fromCenter(
          center: center,
          width: rx * 2 * factor,
          height: ry * 2 * factor,
        ),
        paint,
      );
    }

    // The orbit rail itself.
    final rail = Paint()
      ..style = PaintingStyle.stroke
      ..strokeWidth = 1
      ..color = AppColors.borderMedium;
    canvas.drawOval(
      Rect.fromCenter(center: center, width: rx * 2, height: ry * 2),
      rail,
    );
  }

  @override
  bool shouldRepaint(covariant _OrbitPainter oldDelegate) =>
      oldDelegate.breath != breath ||
      oldDelegate.center != center ||
      oldDelegate.rx != rx ||
      oldDelegate.ry != ry;
}

// ---------------------------------------------------------------------------
// Reduced-motion fallback: a static grid of cards. Drag-to-spark becomes
// tap-and-confirm, per spec §7.
// ---------------------------------------------------------------------------

class _ReducedMotionGrid extends StatelessWidget {
  const _ReducedMotionGrid({
    required this.cards,
    required this.onCardOpen,
    required this.onSpark,
    required this.onStash,
    required this.onPass,
  });

  final List<PulseCard> cards;
  final ValueChanged<PulseCard> onCardOpen;
  final ValueChanged<PulseCard> onSpark;
  final ValueChanged<PulseCard> onStash;
  final ValueChanged<PulseCard> onPass;

  @override
  Widget build(BuildContext context) {
    return GridView.builder(
      padding: AppSpacing.pagePadding.copyWith(top: 16, bottom: 16),
      gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(
        crossAxisCount: 2,
        crossAxisSpacing: 12,
        mainAxisSpacing: 12,
        childAspectRatio: 0.78,
      ),
      itemCount: cards.length,
      itemBuilder: (context, i) {
        final card = cards[i];
        return _ReducedMotionCard(
          card: card,
          onOpen: () => onCardOpen(card),
          onSpark: () => _confirmSpark(context, card),
          onStash: () => onStash(card),
          onPass: () => onPass(card),
        );
      },
    );
  }

  Future<void> _confirmSpark(BuildContext context, PulseCard card) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: AppColors.bgSecondary,
        title: Text('Spark with ${card.profile.firstName}?',
            style: AppTextStyles.h2),
        content: Text(
          'Reduced motion is on, so we replaced drag-to-spark with a confirm.',
          style: AppTextStyles.body,
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: const Text('Cancel'),
          ),
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(true),
            child: const Text('Spark'),
          ),
        ],
      ),
    );
    if (ok == true) onSpark(card);
  }
}

class _ReducedMotionCard extends StatelessWidget {
  const _ReducedMotionCard({
    required this.card,
    required this.onOpen,
    required this.onSpark,
    required this.onStash,
    required this.onPass,
  });

  final PulseCard card;
  final VoidCallback onOpen;
  final VoidCallback onSpark;
  final VoidCallback onStash;
  final VoidCallback onPass;

  @override
  Widget build(BuildContext context) {
    final intentColor = _intentColor(card.profile.intent);
    return InkWell(
      onTap: onOpen,
      borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
      child: Container(
        decoration: BoxDecoration(
          color: AppColors.bgCard,
          border: Border.all(color: AppColors.borderSubtle),
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        ),
        padding: const EdgeInsets.all(12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Container(
              width: 56,
              height: 56,
              decoration: BoxDecoration(
                shape: BoxShape.circle,
                border: Border.all(color: intentColor, width: 2),
                color: AppColors.bgSecondary,
              ),
              child: ClipOval(
                child:
                    card.profile.primaryPhotoUrl != null &&
                            card.profile.primaryPhotoUrl!.isNotEmpty
                        ? Image.network(
                            card.profile.primaryPhotoUrl!,
                            fit: BoxFit.cover,
                          )
                        : Container(
                            alignment: Alignment.center,
                            child: Text(
                              card.profile.firstName.isEmpty
                                  ? '?'
                                  : card.profile.firstName.substring(0, 1),
                              style: AppTextStyles.h3,
                            ),
                          ),
              ),
            ),
            const SizedBox(height: 8),
            Text(
              '${card.profile.firstName}, ${card.profile.age}',
              style: AppTextStyles.h3,
            ),
            const SizedBox(height: 4),
            Text(
              card.profile.intent,
              style: AppTextStyles.labelTiny.copyWith(color: intentColor),
            ),
            const Spacer(),
            Row(
              children: [
                _MiniAction(
                  icon: Icons.bookmark_outline,
                  onTap: onStash,
                  color: AppColors.accentPurple,
                ),
                const SizedBox(width: 8),
                _MiniAction(
                  icon: Icons.bolt,
                  onTap: onSpark,
                  color: AppColors.postbookPrimary,
                ),
                const SizedBox(width: 8),
                _MiniAction(
                  icon: Icons.close,
                  onTap: onPass,
                  color: AppColors.textTertiary,
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

class _MiniAction extends StatelessWidget {
  const _MiniAction({
    required this.icon,
    required this.onTap,
    required this.color,
  });

  final IconData icon;
  final VoidCallback onTap;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return Expanded(
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
        child: Container(
          height: 32,
          decoration: BoxDecoration(
            color: color.withValues(alpha: 0.15),
            borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
          ),
          alignment: Alignment.center,
          child: Icon(icon, color: color, size: 18),
        ),
      ),
    );
  }
}
