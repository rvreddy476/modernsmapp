// Heart toggle for wishlist add/remove. Used on the PDP AppBar and the
// catalog grid card. Optimistically reflects local state via
// `WishlistNotifier.toggle`; on failure the notifier reconciles by
// re-fetching the wishlist.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/data/models/commerce.dart';
import 'package:atpost_app/providers/commerce_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class WishlistButton extends ConsumerWidget {
  const WishlistButton({
    super.key,
    required this.productId,
    this.snapshot,
    this.size = 22,
    this.padded = true,
  });

  final String productId;
  final WishlistItemSnapshot? snapshot;
  final double size;

  /// When true, wraps in an `IconButton` with default tap area; when false,
  /// renders a tighter `InkWell` for use on small grid cards.
  final bool padded;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final notifier = ref.watch(wishlistProvider.notifier);
    // Watch the state so the icon rebuilds when the list mutates.
    ref.watch(wishlistProvider);
    final isSaved = notifier.contains(productId);

    final icon = Icon(
      isSaved ? Icons.favorite : Icons.favorite_border,
      color: isSaved ? AppColors.postgramPrimary : AppColors.textSecondary,
      size: size,
    );

    Future<void> onTap() async {
      await notifier.toggle(productId, snapshot: snapshot);
      if (!context.mounted) return;
      if (isSaved) {
        // Was removed.
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: const Text('Removed from wishlist'),
            action: SnackBarAction(
              label: 'Undo',
              onPressed: () => notifier.add(productId, snapshot: snapshot),
            ),
          ),
        );
      } else {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Added to wishlist')),
        );
      }
    }

    if (padded) {
      return IconButton(
        tooltip: isSaved ? 'Remove from wishlist' : 'Add to wishlist',
        icon: icon,
        onPressed: onTap,
      );
    }
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(999),
      child: Padding(
        padding: const EdgeInsets.all(6),
        child: icon,
      ),
    );
  }
}
