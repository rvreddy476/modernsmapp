// Write a review — Sprint 2.
//
// Entered from the order detail "Rate this product" CTA on a delivered
// item. Backend `POST /v1/commerce/products/:id/reviews` requires a
// `seller_id` + `order_item_id` to mark the review as a verified
// purchase — we receive both via the route extras.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/repositories/commerce_repository.dart';
import 'package:atpost_app/services/commerce_telemetry.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class WriteReviewScreen extends ConsumerStatefulWidget {
  const WriteReviewScreen({
    super.key,
    required this.productId,
    required this.sellerId,
    required this.orderItemId,
    this.productTitle,
  });

  final String productId;
  final String sellerId;
  final String orderItemId;
  final String? productTitle;

  @override
  ConsumerState<WriteReviewScreen> createState() => _WriteReviewScreenState();
}

class _WriteReviewScreenState extends ConsumerState<WriteReviewScreen> {
  int _rating = 0;
  final TextEditingController _titleCtrl = TextEditingController();
  final TextEditingController _bodyCtrl = TextEditingController();
  bool _busy = false;

  static const int _maxTitle = 80;
  static const int _maxBody = 1000;

  @override
  void dispose() {
    _titleCtrl.dispose();
    _bodyCtrl.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    if (_rating == 0) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Please pick a star rating')),
      );
      return;
    }
    setState(() => _busy = true);
    try {
      await ref.read(commerceRepositoryProvider).submitProductReview(
            widget.productId,
            rating: _rating,
            sellerId: widget.sellerId,
            orderItemId: widget.orderItemId,
            title: _titleCtrl.text.trim(),
            body: _bodyCtrl.text.trim(),
          );
      ref.read(commerceTelemetryProvider).reviewSubmitted(
            productId: widget.productId,
            rating: _rating,
          );
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Thanks for your review!')),
      );
      GoRouter.of(context).pop();
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Could not submit review: $e')),
      );
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Write a review', style: AppTextStyles.h2),
      ),
      body: ListView(
        padding: const EdgeInsets.all(AppSpacing.l),
        children: [
          if (widget.productTitle != null) ...[
            Text(widget.productTitle!, style: AppTextStyles.h3),
            const SizedBox(height: AppSpacing.l),
          ],
          Text('Your rating', style: AppTextStyles.label),
          const SizedBox(height: AppSpacing.s),
          Row(
            mainAxisSize: MainAxisSize.min,
            children: List.generate(5, (i) {
              final value = i + 1;
              final filled = _rating >= value;
              return IconButton(
                tooltip: '$value star${value > 1 ? 's' : ''}',
                onPressed: _busy ? null : () => setState(() => _rating = value),
                icon: Icon(
                  filled ? Icons.star : Icons.star_outline,
                  color: filled
                      ? AppColors.statusWarning
                      : AppColors.textMuted,
                  size: 32,
                ),
              );
            }),
          ),
          const SizedBox(height: AppSpacing.xxl),
          Text('Title (optional)', style: AppTextStyles.label),
          const SizedBox(height: AppSpacing.s),
          TextField(
            controller: _titleCtrl,
            maxLength: _maxTitle,
            enabled: !_busy,
            style: AppTextStyles.body,
            decoration: _decoration('Sum it up in a few words'),
          ),
          const SizedBox(height: AppSpacing.l),
          Text('Your review (optional)', style: AppTextStyles.label),
          const SizedBox(height: AppSpacing.s),
          TextField(
            controller: _bodyCtrl,
            maxLines: 6,
            maxLength: _maxBody,
            enabled: !_busy,
            style: AppTextStyles.body,
            decoration: _decoration('What did you like or dislike?'),
          ),
          const SizedBox(height: AppSpacing.l),
          // Photo upload — Sprint 3 (media-service photo upload not wired
          // through commerce repo yet). Stubbed here for layout.
          OutlinedButton.icon(
            onPressed: null,
            icon: const Icon(Icons.add_a_photo_outlined),
            label: const Text('Add photos · coming soon'),
          ),
          const SizedBox(height: AppSpacing.xxl),
          ElevatedButton(
            onPressed: _busy ? null : _submit,
            style: ElevatedButton.styleFrom(
              backgroundColor: AppColors.postbookPrimary,
              padding: const EdgeInsets.symmetric(vertical: 14),
            ),
            child: _busy
                ? const SizedBox(
                    height: 18,
                    width: 18,
                    child: CircularProgressIndicator(
                      strokeWidth: 2,
                      color: Colors.white,
                    ),
                  )
                : const Text('Submit review'),
          ),
        ],
      ),
    );
  }

  InputDecoration _decoration(String hint) {
    return InputDecoration(
      hintText: hint,
      hintStyle: AppTextStyles.body.copyWith(color: AppColors.textMuted),
      filled: true,
      fillColor: AppColors.bgCard,
      border: OutlineInputBorder(
        borderRadius: BorderRadius.circular(AppSpacing.radiusSmall),
        borderSide: const BorderSide(color: AppColors.borderSubtle),
      ),
      enabledBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(AppSpacing.radiusSmall),
        borderSide: const BorderSide(color: AppColors.borderSubtle),
      ),
    );
  }
}
