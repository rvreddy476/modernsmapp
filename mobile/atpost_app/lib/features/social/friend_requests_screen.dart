import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/features/social/widgets/friend_requests_sheet.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// SURFACE 4 (full-screen route variant).
///
/// The Friends home opens the requests surface as a bottom sheet. The
/// `/friend-requests` route — still linked from notifications and the
/// settings screen — renders the exact same surface full-screen via
/// [FriendRequestsSheet] in `asScreen` mode.
class FriendRequestsScreen extends ConsumerWidget {
  const FriendRequestsScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return const Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: SafeArea(
        child: FriendRequestsSheet(asScreen: true),
      ),
    );
  }
}
