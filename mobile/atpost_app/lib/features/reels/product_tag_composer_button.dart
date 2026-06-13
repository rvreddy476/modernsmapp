// Author-gated entry point for the in-video product-tag composer.
//
// Mirrors postbook-ui ProductTagComposerButton. Renders a small
// "Tag products" pill on the reel/watch screen when the viewer is the
// post's author; opens ProductTagComposerSheet as a modal bottom
// sheet on tap. Renders nothing for non-authors so embedding screens
// drop it in without conditional logic.

import 'package:atpost_app/features/reels/product_tag_composer_sheet.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class ProductTagComposerButton extends ConsumerWidget {
  const ProductTagComposerButton({
    super.key,
    required this.postId,
    required this.postAuthorId,
  });

  final String postId;
  final String postAuthorId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final authState = ref.watch(authStateProvider);
    final selfId = authState.valueOrNull?.userId;
    if (selfId == null || selfId != postAuthorId) {
      return const SizedBox.shrink();
    }

    return Material(
      color: const Color(0xFF7C3AED), // violet-600
      shape: const StadiumBorder(),
      elevation: 2,
      child: InkWell(
        customBorder: const StadiumBorder(),
        onTap: () => _open(context),
        child: const Padding(
          padding: EdgeInsets.symmetric(horizontal: 14, vertical: 8),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(Icons.local_offer_outlined, size: 16, color: Colors.white),
              SizedBox(width: 6),
              Text(
                'Tag products',
                style: TextStyle(
                  fontSize: 13,
                  fontWeight: FontWeight.w600,
                  color: Colors.white,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  Future<void> _open(BuildContext context) async {
    await showModalBottomSheet<bool>(
      context: context,
      isScrollControlled: true,
      backgroundColor: Colors.white,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      builder: (_) => ProductTagComposerSheet(postId: postId),
    );
  }
}
