import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/pulse.dart';
import 'package:atpost_app/data/repositories/pulse_repository.dart';
import 'package:atpost_app/features/pulse/pulse_gate.dart';
import 'package:atpost_app/features/pulse/widgets/orbital_canvas.dart';
import 'package:atpost_app/features/pulse/widgets/profile_detail_sheet.dart';
import 'package:atpost_app/features/pulse/widgets/pulse_list_view.dart';
import 'package:atpost_app/features/pulse/widgets/spark_target_picker.dart';
import 'package:atpost_app/features/pulse/widgets/stash_shelf.dart';
import 'package:atpost_app/providers/pulse_providers.dart';
import 'package:atpost_app/services/pulse_auth_service.dart';
import 'package:atpost_app/services/pulse_telemetry.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// Sprint 2 — Pulse hero surface.
///
/// Replaces the S1 swipe deck. Top bar exposes an Orbital / List mode toggle
/// bound to `pulseModeProvider`; the body renders either the `OrbitalCanvas`
/// or `PulseListView`. Profile detail opens as a modal bottom sheet (no
/// route change). All four actions (open / spark / stash / pass) flow into
/// the same parent callbacks so the two modes stay in lockstep.
class PulseDiscoverScreen extends ConsumerStatefulWidget {
  const PulseDiscoverScreen({super.key});

  @override
  ConsumerState<PulseDiscoverScreen> createState() =>
      _PulseDiscoverScreenState();
}

class _PulseDiscoverScreenState extends ConsumerState<PulseDiscoverScreen> {
  bool _gateChecked = false;

  @override
  void initState() {
    super.initState();
    _checkOnboardingGate();
    // Sprint 5 telemetry: every Pulse home open. Fire post-frame so we don't
    // emit during construction, and respect the `mounted` gate.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      ref.read(pulseTelemetryProvider).pulseOpened();
    });
  }

  Future<void> _checkOnboardingGate() async {
    final auth = ref.read(pulseAuthServiceProvider);
    await auth.sessionReady;
    if (!mounted) return;
    if (!auth.isReady) {
      context.go('/pulse/onboarding');
      return;
    }
    setState(() => _gateChecked = true);
  }

  @override
  Widget build(BuildContext context) {
    final mode = ref.watch(pulseModeProvider);
    final pageAsync = ref.watch(pulseTodayProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Pulse', style: AppTextStyles.h2),
        actions: [
          _ModeToggle(
            mode: mode,
            onChanged: (m) =>
                ref.read(pulseModeProvider.notifier).state = m,
          ),
          IconButton(
            tooltip: 'Safety Center',
            onPressed: () => context.push('/pulse/safety'),
            icon: const Icon(
              Icons.shield_outlined,
              color: AppColors.posttubePrimary,
            ),
          ),
          IconButton(
            onPressed: () => context.push('/pulse/matches'),
            icon: const Icon(
              Icons.favorite_border,
              color: AppColors.textPrimary,
            ),
          ),
          IconButton(
            onPressed: () => context.push('/pulse/profile'),
            icon: const Icon(
              Icons.person_outline,
              color: AppColors.textPrimary,
            ),
          ),
        ],
      ),
      body: !_gateChecked
          ? const Center(
              child: CircularProgressIndicator(
                color: AppColors.postbookPrimary,
              ),
            )
          : pageAsync.when(
              loading: () => const Center(
                child: CircularProgressIndicator(
                  color: AppColors.postbookPrimary,
                ),
              ),
              error: (err, _) => _ErrorState(
                message: 'Could not load your pulse.',
                onRetry: () => ref.invalidate(pulseTodayProvider),
              ),
              data: (page) {
                if (page.cohortGated) {
                  return const PulseCohortGatedScreen();
                }
                final body = page.items.isEmpty
                    ? _EmptyState(
                        onRefresh: () =>
                            ref.invalidate(pulseTodayProvider),
                      )
                    : (mode == PulseMode.list
                        ? PulseListView(
                            cards: page.items,
                            onCardOpen: _openDetail,
                            onSpark: _handleSpark,
                            onStash: _handleStash,
                            onPass: _handlePass,
                            onRefresh: () async {
                              ref.invalidate(pulseTodayProvider);
                              // Wait for the new value so the indicator
                              // stays up.
                              await ref.read(pulseTodayProvider.future);
                            },
                          )
                        : OrbitalCanvas(
                            cards: page.items,
                            onCardOpen: _openDetail,
                            onSpark: _handleSpark,
                            onStash: _handleStash,
                            onPass: _handlePass,
                          ));

                return Column(
                  children: [
                    const StashShelf(),
                    Expanded(child: body),
                  ],
                );
              },
            ),
    );
  }

  void _openDetail(PulseCard card) {
    ref
        .read(pulseTelemetryProvider)
        .candidateViewed(candidateUserId: card.profile.userId);
    showPulseProfileDetailSheet(
      context: context,
      card: card,
      onSpark: () => _handleSpark(card),
      onStash: () => _handleStash(card),
      onPass: () => _handlePass(card),
    );
  }

  Future<void> _handleSpark(PulseCard card) async {
    await SparkTargetPicker.show(context, candidate: card);
  }

  Future<void> _handleStash(PulseCard card) async {
    try {
      await ref
          .read(pulseRepositoryProvider)
          .addStash(card.profile.userId);
      ref.read(pulseTelemetryProvider).stashAdded();
      ref.invalidate(stashProvider);
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text('Stashed ${card.profile.firstName}.'),
        ),
      );
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text('Could not stash. Try again.'),
        ),
      );
    }
  }

  void _handlePass(PulseCard card) {
    ref.read(pulseTelemetryProvider).pass();
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text('Passed on ${card.profile.firstName}.'),
      ),
    );
  }
}

class _ModeToggle extends StatelessWidget {
  const _ModeToggle({required this.mode, required this.onChanged});

  final PulseMode mode;
  final ValueChanged<PulseMode> onChanged;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 4),
      child: Container(
        decoration: BoxDecoration(
          color: AppColors.bgSecondary,
          borderRadius: BorderRadius.circular(999),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            _SegmentButton(
              icon: Icons.bubble_chart_outlined,
              tooltip: 'Orbital',
              selected: mode == PulseMode.orbital,
              onTap: () => onChanged(PulseMode.orbital),
            ),
            _SegmentButton(
              icon: Icons.view_list_outlined,
              tooltip: 'List',
              selected: mode == PulseMode.list,
              onTap: () => onChanged(PulseMode.list),
            ),
          ],
        ),
      ),
    );
  }
}

class _SegmentButton extends StatelessWidget {
  const _SegmentButton({
    required this.icon,
    required this.tooltip,
    required this.selected,
    required this.onTap,
  });

  final IconData icon;
  final String tooltip;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Tooltip(
      message: tooltip,
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(999),
        child: Container(
          width: 36,
          height: 32,
          decoration: BoxDecoration(
            color: selected
                ? AppColors.postbookPrimary.withValues(alpha: 0.18)
                : Colors.transparent,
            borderRadius: BorderRadius.circular(999),
          ),
          child: Icon(
            icon,
            size: 18,
            color: selected
                ? AppColors.postbookPrimary
                : AppColors.textTertiary,
          ),
        ),
      ),
    );
  }
}

class _ErrorState extends StatelessWidget {
  const _ErrorState({required this.message, required this.onRetry});
  final String message;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(message, style: AppTextStyles.body),
          const SizedBox(height: 12),
          ElevatedButton(
            onPressed: onRetry,
            style: ElevatedButton.styleFrom(
              backgroundColor: AppColors.postbookPrimary,
            ),
            child: const Text('Retry'),
          ),
        ],
      ),
    );
  }
}

class _EmptyState extends StatelessWidget {
  const _EmptyState({required this.onRefresh});
  final VoidCallback onRefresh;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Text('Your pulse is quiet.', style: AppTextStyles.h2),
          const SizedBox(height: 8),
          Text(
            'New candidates will surface as the day unfolds.',
            style: AppTextStyles.bodySmall,
          ),
          const SizedBox(height: 12),
          ElevatedButton(
            onPressed: onRefresh,
            style: ElevatedButton.styleFrom(
              backgroundColor: AppColors.postbookPrimary,
            ),
            child: const Text('Refresh'),
          ),
        ],
      ),
    );
  }
}
