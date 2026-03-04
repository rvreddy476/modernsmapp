import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/providers/monetization_provider.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class SubscriptionTiersScreen extends ConsumerStatefulWidget {
  const SubscriptionTiersScreen({super.key});

  @override
  ConsumerState<SubscriptionTiersScreen> createState() =>
      _SubscriptionTiersScreenState();
}

class _SubscriptionTiersScreenState
    extends ConsumerState<SubscriptionTiersScreen> {
  @override
  Widget build(BuildContext context) {
    final tiersAsync = ref.watch(myTiersProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        leading: IconButton(
          icon: const Icon(Icons.arrow_back_ios_new,
              color: AppColors.textPrimary, size: 20),
          onPressed: () => context.pop(),
        ),
        title: Text('Subscription Tiers', style: AppTextStyles.h2),
        centerTitle: false,
      ),
      floatingActionButton: FloatingActionButton(
        backgroundColor: AppColors.posttubePrimary,
        onPressed: () => _showAddTierSheet(context),
        child: const Icon(Icons.add, color: Colors.white),
      ),
      body: tiersAsync.when(
        loading: () =>
            const Center(child: CircularProgressIndicator()),
        error: (_, _) => Center(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Text(
                'Could not load tiers. Tap to retry.',
                style: AppTextStyles.bodySmall,
              ),
              const SizedBox(height: 12),
              GestureDetector(
                onTap: () => ref.invalidate(myTiersProvider),
                child: Container(
                  padding: const EdgeInsets.symmetric(
                      horizontal: 16, vertical: 8),
                  decoration: BoxDecoration(
                    gradient: AppColors.posttubeGradient,
                    borderRadius:
                        BorderRadius.circular(AppSpacing.radiusLarge),
                  ),
                  child: Text(
                    'Retry',
                    style: AppTextStyles.label
                        .copyWith(color: Colors.white),
                  ),
                ),
              ),
            ],
          ),
        ),
        data: (tiers) {
          if (tiers.isEmpty) {
            return Center(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  const Icon(Icons.layers_outlined,
                      color: AppColors.textMuted, size: 48),
                  const SizedBox(height: 16),
                  Text(
                    'No tiers yet.',
                    style: AppTextStyles.h3,
                  ),
                  const SizedBox(height: 4),
                  Text(
                    'Add your first subscription tier!',
                    style: AppTextStyles.bodySmall,
                  ),
                  const SizedBox(height: 20),
                  GestureDetector(
                    onTap: () => _showAddTierSheet(context),
                    child: Container(
                      padding: const EdgeInsets.symmetric(
                          horizontal: 20, vertical: 10),
                      decoration: BoxDecoration(
                        gradient: AppColors.posttubeGradient,
                        borderRadius: BorderRadius.circular(
                            AppSpacing.radiusLarge),
                      ),
                      child: Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          const Icon(Icons.add,
                              color: Colors.white, size: 16),
                          const SizedBox(width: 6),
                          Text(
                            'Add Tier',
                            style: AppTextStyles.label
                                .copyWith(color: Colors.white),
                          ),
                        ],
                      ),
                    ),
                  ),
                ],
              ),
            );
          }
          return ListView.separated(
            padding: AppSpacing.pagePadding.copyWith(top: 16, bottom: 100),
            itemCount: tiers.length,
            separatorBuilder: (_, _) => const SizedBox(height: 10),
            itemBuilder: (context, index) {
              final tier = tiers[index];
              return _TierCard(
                tier: tier,
                onEdit: () => _showEditTierSheet(context, tier),
                onDelete: () => _confirmDelete(context, tier),
              );
            },
          );
        },
      ),
    );
  }

  void _showAddTierSheet(BuildContext context) {
    showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      backgroundColor: AppColors.bgSecondary,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(
            top: Radius.circular(AppSpacing.radiusXL)),
      ),
      builder: (ctx) => _TierFormSheet(
        onSubmit: (name, price, benefits) async {
          final api = ref.read(apiClientProvider);
          await api.post('/v1/monetization/tiers', data: {
            'name': name,
            'price': price,
            'benefits': benefits,
          });
          ref.invalidate(myTiersProvider);
        },
      ),
    );
  }

  void _showEditTierSheet(BuildContext context, Map<String, dynamic> tier) {
    showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      backgroundColor: AppColors.bgSecondary,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(
            top: Radius.circular(AppSpacing.radiusXL)),
      ),
      builder: (ctx) => _TierFormSheet(
        initialName: tier['name']?.toString(),
        initialPrice: tier['price']?.toString(),
        initialBenefits: tier['benefits']?.toString(),
        onSubmit: (name, price, benefits) async {
          final api = ref.read(apiClientProvider);
          await api.put('/v1/monetization/tiers/${tier['id']}', data: {
            'name': name,
            'price': price,
            'benefits': benefits,
          });
          ref.invalidate(myTiersProvider);
        },
      ),
    );
  }

  void _confirmDelete(BuildContext context, Map<String, dynamic> tier) {
    showDialog(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: AppColors.bgSecondary,
        title: Text('Delete Tier', style: AppTextStyles.h3),
        content: Text(
          'Are you sure you want to delete "${tier['name']}"? This action cannot be undone.',
          style: AppTextStyles.bodySmall,
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(),
            child: Text('Cancel', style: AppTextStyles.label),
          ),
          TextButton(
            onPressed: () async {
              Navigator.of(ctx).pop();
              final api = ref.read(apiClientProvider);
              await api.delete('/v1/monetization/tiers/${tier['id']}');
              ref.invalidate(myTiersProvider);
            },
            child: Text(
              'Delete',
              style: AppTextStyles.label
                  .copyWith(color: AppColors.liveRed),
            ),
          ),
        ],
      ),
    );
  }
}

class _TierCard extends StatelessWidget {
  final Map<String, dynamic> tier;
  final VoidCallback onEdit;
  final VoidCallback onDelete;

  const _TierCard({
    required this.tier,
    required this.onEdit,
    required this.onDelete,
  });

  @override
  Widget build(BuildContext context) {
    final name = tier['name']?.toString() ?? 'Unnamed Tier';
    final price = tier['price']?.toString() ?? '0';
    final benefits = tier['benefits']?.toString() ?? '';

    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(name, style: AppTextStyles.h3),
                const SizedBox(height: 4),
                Text(
                  '\u20b9$price/mo',
                  style: AppTextStyles.label.copyWith(
                    color: AppColors.posttubePrimary,
                  ),
                ),
                if (benefits.isNotEmpty) ...[
                  const SizedBox(height: 6),
                  Text(
                    benefits,
                    style: AppTextStyles.bodySmall,
                    maxLines: 3,
                    overflow: TextOverflow.ellipsis,
                  ),
                ],
              ],
            ),
          ),
          Column(
            children: [
              IconButton(
                icon: const Icon(Icons.edit_outlined,
                    color: AppColors.textTertiary, size: 20),
                onPressed: onEdit,
                padding: EdgeInsets.zero,
                constraints: const BoxConstraints(),
              ),
              const SizedBox(height: 8),
              IconButton(
                icon: const Icon(Icons.delete_outline,
                    color: AppColors.liveRed, size: 20),
                onPressed: onDelete,
                padding: EdgeInsets.zero,
                constraints: const BoxConstraints(),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

class _TierFormSheet extends StatefulWidget {
  final String? initialName;
  final String? initialPrice;
  final String? initialBenefits;
  final Future<void> Function(String name, String price, String benefits)
      onSubmit;

  const _TierFormSheet({
    this.initialName,
    this.initialPrice,
    this.initialBenefits,
    required this.onSubmit,
  });

  @override
  State<_TierFormSheet> createState() => _TierFormSheetState();
}

class _TierFormSheetState extends State<_TierFormSheet> {
  late final TextEditingController _nameCtrl;
  late final TextEditingController _priceCtrl;
  late final TextEditingController _benefitsCtrl;
  bool _loading = false;

  @override
  void initState() {
    super.initState();
    _nameCtrl =
        TextEditingController(text: widget.initialName ?? '');
    _priceCtrl =
        TextEditingController(text: widget.initialPrice ?? '');
    _benefitsCtrl =
        TextEditingController(text: widget.initialBenefits ?? '');
  }

  @override
  void dispose() {
    _nameCtrl.dispose();
    _priceCtrl.dispose();
    _benefitsCtrl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final isEdit = widget.initialName != null;
    final bottomInset = MediaQuery.of(context).viewInsets.bottom;

    return Padding(
      padding: EdgeInsets.fromLTRB(20, 20, 20, 20 + bottomInset),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Center(
            child: Container(
              width: 40,
              height: 4,
              decoration: BoxDecoration(
                color: AppColors.borderMedium,
                borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
              ),
            ),
          ),
          const SizedBox(height: 16),
          Text(
            isEdit ? 'Edit Tier' : 'New Subscription Tier',
            style: AppTextStyles.h2,
          ),
          const SizedBox(height: 20),
          _inputField('Tier Name', _nameCtrl),
          const SizedBox(height: 12),
          _inputField('Price (₹/month)', _priceCtrl,
              keyboardType: TextInputType.number),
          const SizedBox(height: 12),
          _inputField('Benefits', _benefitsCtrl, maxLines: 3),
          const SizedBox(height: 20),
          SizedBox(
            width: double.infinity,
            child: GestureDetector(
              onTap: _loading ? null : _submit,
              child: Container(
                padding: const EdgeInsets.symmetric(vertical: 14),
                decoration: BoxDecoration(
                  gradient: _loading ? null : AppColors.posttubeGradient,
                  color: _loading ? AppColors.bgCard : null,
                  borderRadius:
                      BorderRadius.circular(AppSpacing.radiusLarge),
                ),
                child: Center(
                  child: _loading
                      ? const SizedBox(
                          width: 20,
                          height: 20,
                          child: CircularProgressIndicator(
                              strokeWidth: 2,
                              color: AppColors.posttubePrimary),
                        )
                      : Text(
                          isEdit ? 'Save Changes' : 'Create',
                          style: AppTextStyles.label
                              .copyWith(color: Colors.white),
                        ),
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }

  Widget _inputField(
    String hint,
    TextEditingController ctrl, {
    TextInputType keyboardType = TextInputType.text,
    int maxLines = 1,
  }) {
    return TextField(
      controller: ctrl,
      keyboardType: keyboardType,
      maxLines: maxLines,
      style: AppTextStyles.body.copyWith(color: AppColors.textPrimary),
      decoration: InputDecoration(
        hintText: hint,
        hintStyle: AppTextStyles.bodySmall,
        filled: true,
        fillColor: AppColors.bgCard,
        contentPadding:
            const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
          borderSide: BorderSide(color: AppColors.borderSubtle),
        ),
        enabledBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
          borderSide: BorderSide(color: AppColors.borderSubtle),
        ),
        focusedBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
          borderSide:
              const BorderSide(color: AppColors.posttubePrimary),
        ),
      ),
    );
  }

  Future<void> _submit() async {
    final name = _nameCtrl.text.trim();
    final price = _priceCtrl.text.trim();
    final benefits = _benefitsCtrl.text.trim();
    if (name.isEmpty || price.isEmpty) return;

    setState(() => _loading = true);
    try {
      await widget.onSubmit(name, price, benefits);
      if (mounted) Navigator.of(context).pop();
    } catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Failed to save tier. Please try again.')),
        );
      }
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }
}
