// All-reviews screen — Sprint 1.
//
// Pulls all reviews via the repo (repo paginates internally; this screen
// shows the first page and offers a "load more" button keyed on the cursor).

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/commerce.dart';
import 'package:atpost_app/data/repositories/commerce_repository.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class ProductReviewsScreen extends ConsumerStatefulWidget {
  const ProductReviewsScreen({super.key, required this.productId});

  final String productId;

  @override
  ConsumerState<ProductReviewsScreen> createState() =>
      _ProductReviewsScreenState();
}

class _ProductReviewsScreenState extends ConsumerState<ProductReviewsScreen> {
  final List<ProductReview> _reviews = [];
  bool _loading = true;
  bool _exhausted = false;
  String? _cursor;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() => _loading = true);
    final repo = ref.read(commerceRepositoryProvider);
    try {
      final page = await repo.getProductReviews(
        widget.productId,
        cursor: _cursor,
      );
      if (!mounted) return;
      setState(() {
        _reviews.addAll(page);
        _cursor = ((int.tryParse(_cursor ?? '0') ?? 0) + page.length).toString();
        _exhausted = page.isEmpty;
      });
    } catch (_) {
      // surfaces as no-op; user can pull-to-refresh.
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Reviews', style: AppTextStyles.h2),
      ),
      body: _reviews.isEmpty && _loading
          ? const Center(
              child:
                  CircularProgressIndicator(color: AppColors.postbookPrimary),
            )
          : _reviews.isEmpty
              ? Center(
                  child: Text('No reviews yet.', style: AppTextStyles.body),
                )
              : ListView.separated(
                  padding: const EdgeInsets.all(AppSpacing.l),
                  itemCount: _reviews.length + (_exhausted ? 0 : 1),
                  separatorBuilder: (_, _) =>
                      const Divider(color: AppColors.borderSubtle, height: 24),
                  itemBuilder: (ctx, i) {
                    if (i == _reviews.length) {
                      return Center(
                        child: TextButton(
                          onPressed: _loading ? null : _load,
                          child: Text(_loading ? 'Loading…' : 'Load more'),
                        ),
                      );
                    }
                    final r = _reviews[i];
                    return Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Row(
                          children: [
                            for (var j = 0; j < 5; j++)
                              Icon(
                                j < r.rating
                                    ? Icons.star
                                    : Icons.star_outline,
                                size: 16,
                                color: AppColors.statusWarning,
                              ),
                            const SizedBox(width: AppSpacing.s),
                            Text(r.buyerName, style: AppTextStyles.label),
                          ],
                        ),
                        if (r.title != null) ...[
                          const SizedBox(height: 4),
                          Text(r.title!, style: AppTextStyles.label),
                        ],
                        if (r.body != null) ...[
                          const SizedBox(height: 4),
                          Text(r.body!, style: AppTextStyles.body),
                        ],
                        const SizedBox(height: 4),
                        Text(
                          '${r.helpfulVotes} found helpful',
                          style: AppTextStyles.bodySmall,
                        ),
                      ],
                    );
                  },
                ),
    );
  }
}
