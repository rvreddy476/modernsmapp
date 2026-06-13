// Phase F4 mobile — RFQ create screen.
//
// Entry point: PDP "Request a custom quote" button. The seller_id +
// variant_id come in as query params; the buyer adds a quantity + an
// optional message + (optionally) picks an organization to bill, then
// submits. On success we land on the RFQ detail screen so the buyer
// can watch for the seller's quote.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/b2b.dart';
import 'package:atpost_app/data/repositories/b2b_repository.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class RFQNewScreen extends ConsumerStatefulWidget {
  const RFQNewScreen({
    super.key,
    required this.sellerId,
    required this.variantId,
  });

  final String sellerId;
  final String variantId;

  @override
  ConsumerState<RFQNewScreen> createState() => _RFQNewScreenState();
}

class _RFQNewScreenState extends ConsumerState<RFQNewScreen> {
  final _qtyController = TextEditingController(text: '10');
  final _messageController = TextEditingController();
  Organization? _selectedOrg;
  bool _submitting = false;

  @override
  void dispose() {
    _qtyController.dispose();
    _messageController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final orgsAsync = ref.watch(myOrganizationsProvider);
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Request a quote', style: AppTextStyles.h2),
      ),
      body: ListView(
        padding: const EdgeInsets.all(AppSpacing.l),
        children: [
          Text(
            'Sellers respond with a custom price + validity window. When you accept, the order is placed at the agreed price.',
            style: AppTextStyles.body,
          ),
          const SizedBox(height: AppSpacing.xxl),
          Text('Quantity', style: AppTextStyles.h3),
          const SizedBox(height: AppSpacing.s),
          TextField(
            controller: _qtyController,
            keyboardType: TextInputType.number,
            decoration: const InputDecoration(
              border: OutlineInputBorder(),
              isDense: true,
              hintText: 'e.g. 100',
            ),
          ),
          const SizedBox(height: AppSpacing.xxl),
          orgsAsync.maybeWhen(
            data: (orgs) => orgs.isEmpty
                ? const SizedBox.shrink()
                : Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text('Bill to (optional)', style: AppTextStyles.h3),
                      const SizedBox(height: AppSpacing.s),
                      DropdownButtonFormField<String>(
                        initialValue: _selectedOrg?.id ?? '',
                        decoration: const InputDecoration(
                          border: OutlineInputBorder(),
                          isDense: true,
                        ),
                        items: [
                          const DropdownMenuItem(
                            value: '',
                            child: Text('Personal'),
                          ),
                          for (final o in orgs)
                            DropdownMenuItem(value: o.id, child: Text(o.name)),
                        ],
                        onChanged: (id) {
                          if (id == null || id.isEmpty) {
                            setState(() => _selectedOrg = null);
                            return;
                          }
                          for (final o in orgs) {
                            if (o.id == id) {
                              setState(() => _selectedOrg = o);
                              return;
                            }
                          }
                        },
                      ),
                      const SizedBox(height: AppSpacing.xxl),
                    ],
                  ),
            orElse: () => const SizedBox.shrink(),
          ),
          Text('Notes for seller (optional)', style: AppTextStyles.h3),
          const SizedBox(height: AppSpacing.s),
          TextField(
            controller: _messageController,
            maxLines: 4,
            decoration: const InputDecoration(
              border: OutlineInputBorder(),
              isDense: true,
              hintText: 'Delivery deadlines, packaging needs, etc.',
            ),
          ),
        ],
      ),
      bottomNavigationBar: SafeArea(
        child: Container(
          padding: const EdgeInsets.all(AppSpacing.l),
          decoration: const BoxDecoration(
            color: AppColors.bgPrimary,
            border: Border(top: BorderSide(color: AppColors.borderSubtle)),
          ),
          child: ElevatedButton(
            onPressed: _submitting ? null : _submit,
            style: ElevatedButton.styleFrom(
              backgroundColor: AppColors.postbookPrimary,
              padding: const EdgeInsets.symmetric(vertical: 14),
            ),
            child: Text(_submitting ? 'Sending…' : 'Send request'),
          ),
        ),
      ),
    );
  }

  Future<void> _submit() async {
    final qty = int.tryParse(_qtyController.text.trim()) ?? 0;
    if (qty <= 0) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Enter a quantity greater than zero')),
      );
      return;
    }
    setState(() => _submitting = true);
    try {
      final rfq = await ref.read(b2bRepositoryProvider).createRFQ(
        sellerId: widget.sellerId,
        items: [
          (
            variantId: widget.variantId,
            quantity: qty,
            notes: null,
          ),
        ],
        message: _messageController.text.trim(),
        organizationId: _selectedOrg?.id,
      );
      ref.invalidate(myRFQsProvider);
      if (!mounted) return;
      GoRouter.of(context).pushReplacement('/rfq/${rfq.id}');
    } catch (e) {
      if (!mounted) return;
      setState(() => _submitting = false);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Failed to send: $e')),
      );
    }
  }
}
