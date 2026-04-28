import 'package:atpost_app/data/repositories/monetization_repository.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Fan-side tier picker. Lists the creator's active tiers and
/// subscribes on tap. Mirrors the web TierPicker modal.
class TierPickerSheet extends ConsumerStatefulWidget {
  final String creatorId;
  final String? creatorName;

  const TierPickerSheet({
    super.key,
    required this.creatorId,
    this.creatorName,
  });

  /// Show as a bottom sheet. Returns the chosen tier ID on success.
  static Future<String?> show(
    BuildContext context, {
    required String creatorId,
    String? creatorName,
  }) {
    return showModalBottomSheet<String>(
      context: context,
      isScrollControlled: true,
      backgroundColor: Colors.transparent,
      builder: (_) => TierPickerSheet(
        creatorId: creatorId,
        creatorName: creatorName,
      ),
    );
  }

  @override
  ConsumerState<TierPickerSheet> createState() => _TierPickerSheetState();
}

class _TierPickerSheetState extends ConsumerState<TierPickerSheet> {
  late Future<List<CreatorTier>> _tiersFuture;
  String? _subscribingTierId;
  String? _error;

  @override
  void initState() {
    super.initState();
    _tiersFuture = ref
        .read(monetizationRepositoryProvider)
        .getCreatorTiers(widget.creatorId);
  }

  Future<void> _pick(CreatorTier t) async {
    setState(() {
      _subscribingTierId = t.id;
      _error = null;
    });
    try {
      await ref.read(monetizationRepositoryProvider).subscribe(
        creatorId: widget.creatorId,
        tierId: t.id,
      );
      if (mounted) Navigator.of(context).pop(t.id);
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _subscribingTierId = null;
        _error = 'Failed to subscribe';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Container(
      decoration: BoxDecoration(
        color: theme.colorScheme.surface,
        borderRadius: const BorderRadius.vertical(top: Radius.circular(20)),
      ),
      constraints: BoxConstraints(
        maxHeight: MediaQuery.of(context).size.height * 0.85,
      ),
      padding: const EdgeInsets.all(20),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      'Become a member',
                      style: theme.textTheme.titleLarge,
                    ),
                    const SizedBox(height: 4),
                    Text(
                      widget.creatorName != null
                          ? 'Support ${widget.creatorName} and unlock members-only content'
                          : 'Pick a tier',
                      style: theme.textTheme.bodySmall?.copyWith(
                        color: theme.hintColor,
                      ),
                    ),
                  ],
                ),
              ),
              IconButton(
                onPressed: () => Navigator.of(context).pop(),
                icon: const Icon(Icons.close),
              ),
            ],
          ),
          const SizedBox(height: 12),
          Flexible(
            child: FutureBuilder<List<CreatorTier>>(
              future: _tiersFuture,
              builder: (context, snap) {
                if (snap.connectionState == ConnectionState.waiting) {
                  return const Padding(
                    padding: EdgeInsets.all(40),
                    child: Center(child: CircularProgressIndicator()),
                  );
                }
                if (snap.hasError) {
                  return Padding(
                    padding: const EdgeInsets.all(24),
                    child: Center(
                      child: Text(
                        "Couldn't load tiers. Please try again.",
                        style: TextStyle(color: theme.colorScheme.error),
                      ),
                    ),
                  );
                }
                final tiers = snap.data ?? const <CreatorTier>[];
                if (tiers.isEmpty) {
                  return const Padding(
                    padding: EdgeInsets.all(40),
                    child: Center(
                      child: Text(
                        "This creator hasn't set up any membership tiers yet.",
                        textAlign: TextAlign.center,
                      ),
                    ),
                  );
                }
                return ListView.separated(
                  shrinkWrap: true,
                  itemCount: tiers.length,
                  separatorBuilder: (_, _) => const SizedBox(height: 10),
                  itemBuilder: (_, i) {
                    final t = tiers[i];
                    final isSubscribing = _subscribingTierId == t.id;
                    return _TierCard(
                      tier: t,
                      isSubscribing: isSubscribing,
                      onPick: () => _pick(t),
                    );
                  },
                );
              },
            ),
          ),
          if (_error != null) ...[
            const SizedBox(height: 8),
            Text(
              _error!,
              style: TextStyle(color: theme.colorScheme.error),
            ),
          ],
        ],
      ),
    );
  }
}

class _TierCard extends StatelessWidget {
  final CreatorTier tier;
  final bool isSubscribing;
  final VoidCallback onPick;

  const _TierCard({
    required this.tier,
    required this.isSubscribing,
    required this.onPick,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Container(
      decoration: BoxDecoration(
        border: Border.all(color: theme.dividerColor),
        borderRadius: BorderRadius.circular(12),
      ),
      padding: const EdgeInsets.all(14),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  tier.name,
                  style: theme.textTheme.titleMedium?.copyWith(
                    fontWeight: FontWeight.w700,
                  ),
                ),
                Text(
                  '₹${tier.priceRupees.toStringAsFixed(2)} / month',
                  style: theme.textTheme.bodySmall?.copyWith(
                    color: theme.hintColor,
                  ),
                ),
                if (tier.perks.isNotEmpty) ...[
                  const SizedBox(height: 6),
                  ...tier.perks.map(
                    (p) => Padding(
                      padding: const EdgeInsets.only(top: 2),
                      child: Row(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          const Text('• '),
                          Expanded(
                            child: Text(
                              p,
                              style: theme.textTheme.bodySmall,
                            ),
                          ),
                        ],
                      ),
                    ),
                  ),
                ],
              ],
            ),
          ),
          const SizedBox(width: 8),
          FilledButton(
            onPressed: isSubscribing ? null : onPick,
            child: Text(isSubscribing ? '…' : 'Join'),
          ),
        ],
      ),
    );
  }
}
