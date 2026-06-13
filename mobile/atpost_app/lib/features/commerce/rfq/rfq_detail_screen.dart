// Phase F4 mobile — RFQ detail. Shows the seller's quote with the
// per-line price breakdown. When a live quote exists, the buyer picks
// a delivery address + payment method and accepts; backend creates an
// order at the quoted prices (bypasses priceCart) and we route to the
// new order page.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/b2b.dart';
import 'package:atpost_app/data/models/commerce.dart';
import 'package:atpost_app/data/repositories/b2b_repository.dart';
import 'package:atpost_app/providers/commerce_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class RFQDetailScreen extends ConsumerStatefulWidget {
  const RFQDetailScreen({super.key, required this.rfqId});

  final String rfqId;

  @override
  ConsumerState<RFQDetailScreen> createState() => _RFQDetailScreenState();
}

class _RFQDetailScreenState extends ConsumerState<RFQDetailScreen> {
  Address? _address;
  String _paymentMethod = 'prepaid';
  bool _accepting = false;

  @override
  Widget build(BuildContext context) {
    final detailAsync = ref.watch(rfqDetailProvider(widget.rfqId));
    _address ??= ref.watch(defaultAddressProvider);
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('RFQ', style: AppTextStyles.h2),
      ),
      body: detailAsync.when(
        loading: () => const Center(
          child: CircularProgressIndicator(color: AppColors.postbookPrimary),
        ),
        error: (e, _) => Center(
          child: Padding(
            padding: const EdgeInsets.all(AppSpacing.xxl),
            child: Text(
              'Could not load RFQ.\n$e',
              style: AppTextStyles.body,
              textAlign: TextAlign.center,
            ),
          ),
        ),
        data: (detail) => _buildBody(context, detail),
      ),
    );
  }

  Widget _buildBody(BuildContext context, RFQDetail detail) {
    final r = detail.rfq;
    final liveQuote = detail.liveQuote;
    return ListView(
      padding: const EdgeInsets.all(AppSpacing.l),
      children: [
        Text('RFQ ${r.id.substring(0, 8)}…', style: AppTextStyles.h3),
        const SizedBox(height: AppSpacing.xs),
        Text(
          'Status: ${r.status} · Expires ${_fmtDate(r.expiresAt)}',
          style: AppTextStyles.bodySmall,
        ),
        if (r.messageText != null && r.messageText!.isNotEmpty) ...[
          const SizedBox(height: AppSpacing.xxl),
          Text('Your message', style: AppTextStyles.h3),
          const SizedBox(height: AppSpacing.xs),
          Text(r.messageText!, style: AppTextStyles.body),
        ],
        const SizedBox(height: AppSpacing.xxl),
        Text('Items', style: AppTextStyles.h3),
        const SizedBox(height: AppSpacing.s),
        for (final it in detail.items)
          Padding(
            padding: const EdgeInsets.symmetric(vertical: 4),
            child: Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                Text(
                  'Variant ${it.variantId.substring(0, 8)}…',
                  style: AppTextStyles.body,
                ),
                Text('qty ${it.quantity}', style: AppTextStyles.body),
              ],
            ),
          ),
        if (liveQuote != null) ...[
          const SizedBox(height: AppSpacing.xxl),
          _QuoteCard(quote: liveQuote),
        ],
        if (r.status == 'quoted' && liveQuote != null) ...[
          const SizedBox(height: AppSpacing.xxl),
          _buildAcceptForm(context, liveQuote),
        ],
        if (r.status == 'requested') ...[
          const SizedBox(height: AppSpacing.xxl),
          Container(
            padding: const EdgeInsets.all(AppSpacing.l),
            decoration: BoxDecoration(
              color: AppColors.statusWarning.withValues(alpha: 0.12),
              borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
              border: Border.all(color: AppColors.statusWarning),
            ),
            child: Text(
              'Waiting for the seller to send a quote. RFQs expire automatically after the window closes.',
              style: AppTextStyles.bodySmall,
            ),
          ),
        ],
      ],
    );
  }

  Widget _buildAcceptForm(BuildContext context, RFQQuote quote) {
    final addrsAsync = ref.watch(addressesProvider);
    return Container(
      padding: const EdgeInsets.all(AppSpacing.l),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('Accept this quote', style: AppTextStyles.h3),
          const SizedBox(height: AppSpacing.s),
          addrsAsync.when(
            loading: () => const Padding(
              padding: EdgeInsets.symmetric(vertical: AppSpacing.s),
              child: LinearProgressIndicator(),
            ),
            error: (e, _) => Text('Could not load addresses: $e',
                style: AppTextStyles.bodySmall),
            data: (addrs) {
              if (addrs.isEmpty) {
                return Text(
                  'Add a delivery address before accepting.',
                  style: AppTextStyles.bodySmall,
                );
              }
              return DropdownButtonFormField<String>(
                initialValue: _address?.id ?? addrs.first.id,
                decoration: const InputDecoration(
                  border: OutlineInputBorder(),
                  isDense: true,
                  labelText: 'Deliver to',
                ),
                items: [
                  for (final a in addrs)
                    DropdownMenuItem(
                      value: a.id,
                      child: Text(
                        '${a.fullName} · ${a.city}, ${a.postalCode}',
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                ],
                onChanged: (id) {
                  if (id == null) return;
                  for (final a in addrs) {
                    if (a.id == id) {
                      setState(() => _address = a);
                      return;
                    }
                  }
                },
              );
            },
          ),
          const SizedBox(height: AppSpacing.l),
          DropdownButtonFormField<String>(
            initialValue: _paymentMethod,
            decoration: const InputDecoration(
              border: OutlineInputBorder(),
              isDense: true,
              labelText: 'Payment',
            ),
            items: const [
              DropdownMenuItem(value: 'prepaid', child: Text('Pay online')),
              DropdownMenuItem(
                  value: 'cod', child: Text('Cash on Delivery')),
              DropdownMenuItem(
                  value: 'credit',
                  child: Text('Pay on invoice (Net N — org only)')),
            ],
            onChanged: (v) => setState(() => _paymentMethod = v ?? 'prepaid'),
          ),
          const SizedBox(height: AppSpacing.l),
          Row(
            children: [
              Expanded(
                child: OutlinedButton(
                  onPressed: _accepting ? null : () => _reject(context),
                  style: OutlinedButton.styleFrom(
                    foregroundColor: AppColors.statusError,
                    side: BorderSide(
                      color: AppColors.statusError.withValues(alpha: 0.4),
                    ),
                    padding: const EdgeInsets.symmetric(vertical: 14),
                  ),
                  child: const Text('Reject'),
                ),
              ),
              const SizedBox(width: AppSpacing.l),
              Expanded(
                flex: 2,
                child: ElevatedButton(
                  onPressed: _accepting || _address == null
                      ? null
                      : () => _accept(context, quote),
                  style: ElevatedButton.styleFrom(
                    backgroundColor: AppColors.postbookPrimary,
                    padding: const EdgeInsets.symmetric(vertical: 14),
                  ),
                  child: Text(_accepting
                      ? 'Accepting…'
                      : 'Accept · ₹${quote.quotedTotal.toStringAsFixed(0)}'),
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }

  Future<void> _accept(BuildContext context, RFQQuote quote) async {
    final addr = _address;
    if (addr == null) return;
    setState(() => _accepting = true);
    try {
      final order = await ref.read(b2bRepositoryProvider).acceptQuote(
        rfqId: widget.rfqId,
        quoteId: quote.id,
        addressId: addr.id,
        paymentMethod: _paymentMethod,
      );
      ref.invalidate(rfqDetailProvider(widget.rfqId));
      ref.invalidate(myRFQsProvider);
      if (!mounted) return;
      GoRouter.of(context).pushReplacement(
        '/commerce/orders/${order.id}?placed=1',
      );
    } catch (e) {
      if (!mounted) return;
      setState(() => _accepting = false);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Accept failed: $e')),
      );
    }
  }

  Future<void> _reject(BuildContext context) async {
    final reason = await showDialog<String>(
      context: context,
      builder: (ctx) {
        final c = TextEditingController();
        return AlertDialog(
          title: const Text('Reject quote'),
          content: TextField(
            controller: c,
            decoration: const InputDecoration(
              labelText: 'Reason (optional)',
              border: OutlineInputBorder(),
            ),
            maxLines: 3,
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(ctx).pop(),
              child: const Text('Cancel'),
            ),
            FilledButton(
              onPressed: () => Navigator.of(ctx).pop(c.text.trim()),
              style: FilledButton.styleFrom(
                backgroundColor: AppColors.statusError,
              ),
              child: const Text('Reject'),
            ),
          ],
        );
      },
    );
    if (reason == null) return;
    try {
      await ref.read(b2bRepositoryProvider).rejectRFQ(widget.rfqId, reason: reason);
      ref.invalidate(rfqDetailProvider(widget.rfqId));
      ref.invalidate(myRFQsProvider);
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('RFQ rejected')),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Reject failed: $e')),
      );
    }
  }

  static String _fmtDate(DateTime d) {
    final mm = d.month.toString().padLeft(2, '0');
    final dd = d.day.toString().padLeft(2, '0');
    return '$dd/$mm/${d.year}';
  }
}

class _QuoteCard extends StatelessWidget {
  const _QuoteCard({required this.quote});

  final RFQQuote quote;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(AppSpacing.l),
      decoration: BoxDecoration(
        color: AppColors.statusSuccess.withValues(alpha: 0.08),
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        border: Border.all(color: AppColors.statusSuccess.withValues(alpha: 0.4)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('Seller\'s quote', style: AppTextStyles.h3),
          const SizedBox(height: AppSpacing.xs),
          Text(
            '₹${quote.quotedTotal.toStringAsFixed(2)}',
            style: AppTextStyles.h2.copyWith(
              color: AppColors.statusSuccess,
            ),
          ),
          Text(
            'Valid until ${_fmtDateTime(quote.expiresAt)}',
            style: AppTextStyles.bodySmall,
          ),
          const SizedBox(height: AppSpacing.s),
          for (final lp in quote.linePrices)
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 2),
              child: Row(
                mainAxisAlignment: MainAxisAlignment.spaceBetween,
                children: [
                  Expanded(
                    child: Text(
                      'Variant ${lp.variantId.substring(0, 8)}… × ${lp.quantity}',
                      style: AppTextStyles.bodySmall,
                    ),
                  ),
                  Text(
                    '₹${lp.lineTotal.toStringAsFixed(0)} (₹${lp.unitPrice.toStringAsFixed(0)}/u)',
                    style: AppTextStyles.bodySmall,
                  ),
                ],
              ),
            ),
        ],
      ),
    );
  }

  static String _fmtDateTime(DateTime d) {
    final mm = d.month.toString().padLeft(2, '0');
    final dd = d.day.toString().padLeft(2, '0');
    final hh = d.hour.toString().padLeft(2, '0');
    final mi = d.minute.toString().padLeft(2, '0');
    return '$dd/$mm/${d.year} $hh:$mi';
  }
}
