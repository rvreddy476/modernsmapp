import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/pulse.dart';
import 'package:atpost_app/data/repositories/pulse_repository.dart';
import 'package:atpost_app/providers/pulse_safety_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// Sprint 4 — list of pending vouch requests where I'm the voucher.
///
/// Renders the subset of `vouchesForMeProvider` whose status == pending
/// AND whose voucher is *not* the viewer (those are pending requests where
/// somebody asked the viewer to vouch).
class VouchInboxScreen extends ConsumerWidget {
  const VouchInboxScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final async = ref.watch(vouchesForMeProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Vouch requests', style: AppTextStyles.h2),
        leading: IconButton(
          onPressed: () => context.pop(),
          icon: const Icon(Icons.arrow_back_ios_new_rounded,
              color: AppColors.textPrimary, size: 18),
        ),
      ),
      body: async.when(
        loading: () => const Center(
          child: CircularProgressIndicator(
            color: AppColors.postbookPrimary,
          ),
        ),
        error: (_, _) => _ErrorState(
          onRetry: () => ref.invalidate(vouchesForMeProvider),
        ),
        data: (rows) {
          final pending =
              rows.where((v) => v.isPending).toList(growable: false);
          if (pending.isEmpty) {
            return Center(
              child: Padding(
                padding: AppSpacing.pagePadding,
                child: Text(
                  'You have no vouch requests to act on right now.',
                  textAlign: TextAlign.center,
                  style: AppTextStyles.bodySmall,
                ),
              ),
            );
          }
          return RefreshIndicator(
            onRefresh: () async => ref.invalidate(vouchesForMeProvider),
            child: ListView.separated(
              padding:
                  AppSpacing.pagePadding.copyWith(top: 14, bottom: 24),
              itemCount: pending.length,
              separatorBuilder: (_, _) => const SizedBox(height: 10),
              itemBuilder: (_, index) => _PendingTile(vouch: pending[index]),
            ),
          );
        },
      ),
    );
  }
}

class _PendingTile extends ConsumerStatefulWidget {
  const _PendingTile({required this.vouch});

  final Vouch vouch;

  @override
  ConsumerState<_PendingTile> createState() => _PendingTileState();
}

class _PendingTileState extends ConsumerState<_PendingTile> {
  bool _busy = false;

  Future<void> _decide(String decision) async {
    setState(() => _busy = true);
    try {
      await ref
          .read(pulseRepositoryProvider)
          .decideVouch(widget.vouch.id, decision);
      if (!mounted) return;
      ref.invalidate(vouchesForMeProvider);
      ref.invalidate(vouchesSentProvider);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(decision == 'accept'
              ? 'Vouch accepted.'
              : 'Vouch declined.'),
        ),
      );
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not update this vouch.')),
      );
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final v = widget.vouch;
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              CircleAvatar(
                radius: 18,
                backgroundColor: AppColors.bgCardHover,
                child: Text(
                  (v.voucheeName ?? v.voucheeId).isNotEmpty
                      ? (v.voucheeName ?? v.voucheeId)[0].toUpperCase()
                      : '?',
                  style: AppTextStyles.label,
                ),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      v.voucheeName ??
                          'Someone you know (${v.voucheeId.substring(0, 6)})',
                      style: AppTextStyles.h3,
                    ),
                    const SizedBox(height: 2),
                    Text(
                      'Asked you to vouch as ${v.relationship}.',
                      style: AppTextStyles.bodySmall,
                    ),
                  ],
                ),
              ),
            ],
          ),
          if (v.note != null && v.note!.isNotEmpty) ...[
            const SizedBox(height: 10),
            Text('"${v.note!}"',
                style: AppTextStyles.bodySmall.copyWith(
                    fontStyle: FontStyle.italic)),
          ],
          const SizedBox(height: 12),
          Row(
            children: [
              Expanded(
                child: FilledButton.tonal(
                  onPressed: _busy ? null : () => _decide('decline'),
                  child: const Text('Decline'),
                ),
              ),
              const SizedBox(width: 10),
              Expanded(
                child: FilledButton(
                  onPressed: _busy ? null : () => _decide('accept'),
                  style: FilledButton.styleFrom(
                    backgroundColor: AppColors.postbookPrimary,
                  ),
                  child: const Text('Accept'),
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

class _ErrorState extends StatelessWidget {
  const _ErrorState({required this.onRetry});

  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: AppSpacing.pagePadding,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text('Could not load vouch requests.',
                style: AppTextStyles.body),
            const SizedBox(height: 12),
            FilledButton.tonal(
                onPressed: onRetry, child: const Text('Retry')),
          ],
        ),
      ),
    );
  }
}
