import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/features/chat/chat_list_screen.dart';
import 'package:atpost_app/features/home/home_feed_screen.dart';
import 'package:atpost_app/features/profile/profile_screen.dart';
import 'package:atpost_app/features/reels/reels_screen.dart';
import 'package:atpost_app/features/services/explore_bottom_sheet.dart';
import 'package:atpost_app/features/shell/shell_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_animate/flutter_animate.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class ShellScaffold extends ConsumerWidget {
  const ShellScaffold({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final currentTab = ref.watch(shellTabProvider);
    final showCreateMenu = ref.watch(createMenuOpenProvider);

    final pages = <Widget>[
      const HomeFeedScreen(),
      const ChatListScreen(),
      const SizedBox.shrink(),
      const ReelsScreen(),
      const ProfileScreen(),
    ];

    return Scaffold(
      body: Stack(
        children: [
          IndexedStack(index: currentTab, children: pages),
          if (showCreateMenu)
            Positioned.fill(
              child: GestureDetector(
                onTap: () =>
                    ref.read(createMenuOpenProvider.notifier).state = false,
                child: Container(color: Colors.black.withValues(alpha: 0.22)),
              ),
            ),
          if (showCreateMenu)
            Positioned(
              left: 0,
              right: 0,
              bottom: 92,
              child: const _CreateMenu(),
            ),
        ],
      ),
      bottomNavigationBar: const _BottomNav(),
    );
  }
}

class _BottomNav extends ConsumerWidget {
  const _BottomNav();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final current = ref.watch(shellTabProvider);

    return SafeArea(
      top: false,
      child: Container(
        margin: const EdgeInsets.fromLTRB(16, 0, 16, 16),
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
        decoration: BoxDecoration(
          color: AppColors.bgSecondary,
          borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Row(
          mainAxisAlignment: MainAxisAlignment.spaceAround,
          children: [
            _NavItem(
              icon: Icons.home_rounded,
              label: 'Home',
              active: current == 0,
              onTap: () {
                ref.read(shellTabProvider.notifier).state = 0;
                ref.read(createMenuOpenProvider.notifier).state = false;
              },
            ),
            _NavItem(
              icon: Icons.chat_bubble_outline_rounded,
              label: 'Messenger',
              active: current == 1,
              onTap: () {
                ref.read(shellTabProvider.notifier).state = 1;
                ref.read(createMenuOpenProvider.notifier).state = false;
              },
            ),
            _CreateButton(
              onTap: () {
                final open = ref.read(createMenuOpenProvider);
                ref.read(createMenuOpenProvider.notifier).state = !open;
              },
            ),
            _NavItem(
              icon: Icons.movie_creation_outlined,
              label: 'Reels',
              active: current == 3,
              onTap: () {
                ref.read(shellTabProvider.notifier).state = 3;
                ref.read(createMenuOpenProvider.notifier).state = false;
              },
            ),
            _NavItem(
              icon: Icons.explore_outlined,
              label: 'Explore',
              active: false,
              onTap: () {
                ref.read(createMenuOpenProvider.notifier).state = false;
                showExploreBottomSheet(context);
              },
            ),
          ],
        ),
      ),
    );
  }
}

class _NavItem extends StatelessWidget {
  const _NavItem({
    required this.icon,
    required this.label,
    required this.active,
    required this.onTap,
  });

  final IconData icon;
  final String label;
  final bool active;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final color = active ? AppColors.postbookPrimary : AppColors.textDimmest;
    return GestureDetector(
      onTap: onTap,
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, size: 22, color: color),
          const SizedBox(height: 4),
          Text(
            label,
            style: AppTextStyles.labelSmall.copyWith(
              color: color,
              fontWeight: active ? FontWeight.w700 : FontWeight.w500,
            ),
          ),
          const SizedBox(height: 4),
          AnimatedContainer(
            duration: const Duration(milliseconds: 220),
            width: active ? 12 : 0,
            height: 3,
            decoration: BoxDecoration(
              color: AppColors.postbookPrimary,
              borderRadius: BorderRadius.circular(999),
            ),
          ),
        ],
      ),
    );
  }
}

class _CreateButton extends StatelessWidget {
  const _CreateButton({required this.onTap});

  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: Transform.translate(
        offset: const Offset(0, -10),
        child:
            Container(
              width: 50,
              height: 50,
              decoration: BoxDecoration(
                gradient: AppColors.ctaGradient,
                borderRadius: BorderRadius.circular(16),
                boxShadow: const [
                  BoxShadow(
                    color: Color(0x66FF6B35),
                    blurRadius: 16,
                    offset: Offset(0, 6),
                  ),
                ],
              ),
              child: const Icon(Icons.add, color: Colors.white),
            ).animate().scale(
              duration: 220.ms,
              begin: const Offset(1, 1),
              end: const Offset(1.05, 1.05),
            ),
      ),
    );
  }
}

class _CreateMenu extends ConsumerWidget {
  const _CreateMenu();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    void openRoute(String route) {
      final router = GoRouter.of(context);
      ref.read(createMenuOpenProvider.notifier).state = false;
      router.push(route);
    }

    return Container(
          margin: const EdgeInsets.symmetric(horizontal: 18),
          padding: const EdgeInsets.all(10),
          decoration: BoxDecoration(
            color: AppColors.bgTertiary,
            borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: Row(
            mainAxisAlignment: MainAxisAlignment.spaceAround,
            children: [
              _CreateItem(
                label: 'Post',
                icon: Icons.edit_note,
                color: AppColors.postbookPrimary,
                onTap: () => openRoute('/create'),
              ),
              _CreateItem(
                label: 'Reel',
                icon: Icons.movie_filter,
                color: AppColors.postgramPrimary,
                onTap: () => openRoute('/reels'),
              ),
              _CreateItem(
                label: 'Video',
                icon: Icons.live_tv,
                color: AppColors.posttubePrimary,
                onTap: () => openRoute('/posttube'),
              ),
              _CreateItem(
                label: 'Live',
                icon: Icons.podcasts,
                color: AppColors.liveRed,
                onTap: () => openRoute('/live'),
              ),
            ],
          ),
        )
        .animate()
        .fadeIn(duration: 220.ms, curve: Curves.easeOut)
        .slideY(begin: 0.2, end: 0, duration: 220.ms, curve: Curves.easeOut);
  }
}

class _CreateItem extends StatelessWidget {
  const _CreateItem({
    required this.label,
    required this.icon,
    required this.color,
    this.onTap,
  });

  final String label;
  final IconData icon;
  final Color color;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    return Material(
      color: Colors.transparent,
      child: InkWell(
        borderRadius: BorderRadius.circular(14),
        onTap: onTap,
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Container(
                width: 42,
                height: 42,
                decoration: BoxDecoration(
                  color: color.withValues(alpha: 0.2),
                  borderRadius: BorderRadius.circular(14),
                ),
                child: Icon(icon, color: color, size: 20),
              ),
              const SizedBox(height: 4),
              Text(
                label,
                style: AppTextStyles.labelSmall.copyWith(
                  color: AppColors.textSecondary,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
