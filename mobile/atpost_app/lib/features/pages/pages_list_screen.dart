import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/business_page.dart';
import 'package:atpost_app/providers/pages_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// Pages discovery — browse approved public pages + a tab for the pages you own.
class PagesListScreen extends ConsumerStatefulWidget {
  const PagesListScreen({super.key});

  @override
  ConsumerState<PagesListScreen> createState() => _PagesListScreenState();
}

class _PagesListScreenState extends ConsumerState<PagesListScreen> {
  String _search = '';
  bool _mine = false;

  @override
  Widget build(BuildContext context) {
    final discoverAsync = ref.watch(pagesDiscoverProvider(_search));
    final mineAsync = ref.watch(myPagesProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Pages', style: AppTextStyles.h2),
        actions: [
          IconButton(
            icon: const Icon(Icons.add, color: AppColors.postbookPrimary),
            tooltip: 'Create page',
            onPressed: () => context.push('/pages/create'),
          ),
        ],
      ),
      body: Column(
        children: [
          // Discover / Mine toggle
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
            child: Row(
              children: [
                _Tab(label: 'Discover', active: !_mine, onTap: () => setState(() => _mine = false)),
                const SizedBox(width: 8),
                _Tab(label: 'My Pages', active: _mine, onTap: () => setState(() => _mine = true)),
              ],
            ),
          ),
          if (!_mine)
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 0, 16, 8),
              child: TextField(
                onChanged: (v) => setState(() => _search = v),
                style: AppTextStyles.body,
                decoration: InputDecoration(
                  hintText: 'Search pages…',
                  hintStyle: AppTextStyles.body.copyWith(color: AppColors.textTertiary),
                  prefixIcon: const Icon(Icons.search, size: 18, color: AppColors.textTertiary),
                  filled: true,
                  fillColor: AppColors.bgSecondary,
                  contentPadding: const EdgeInsets.symmetric(vertical: 4),
                  border: OutlineInputBorder(borderRadius: BorderRadius.circular(12), borderSide: BorderSide.none),
                ),
              ),
            ),
          Expanded(
            child: _mine
                ? mineAsync.when(
                    loading: () => const _Loading(),
                    error: (_, _) => const _ErrorState(),
                    data: (pages) => _PageGrid(pages: pages, emptyLabel: 'You have no pages yet. Tap + to create one.'),
                  )
                : discoverAsync.when(
                    loading: () => const _Loading(),
                    error: (_, _) => const _ErrorState(),
                    data: (pages) => _PageGrid(pages: pages, emptyLabel: 'No pages found.'),
                  ),
          ),
        ],
      ),
    );
  }
}

class _Tab extends StatelessWidget {
  const _Tab({required this.label, required this.active, required this.onTap});
  final String label;
  final bool active;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
        decoration: BoxDecoration(
          color: active ? AppColors.postbookPrimary : AppColors.bgSecondary,
          borderRadius: BorderRadius.circular(999),
        ),
        child: Text(
          label,
          style: AppTextStyles.label.copyWith(color: active ? Colors.white : AppColors.textSecondary),
        ),
      ),
    );
  }
}

class _PageGrid extends StatelessWidget {
  const _PageGrid({required this.pages, required this.emptyLabel});
  final List<BusinessPage> pages;
  final String emptyLabel;

  @override
  Widget build(BuildContext context) {
    if (pages.isEmpty) {
      return Center(child: Text(emptyLabel, style: AppTextStyles.body.copyWith(color: AppColors.textTertiary)));
    }
    return ListView.separated(
      padding: const EdgeInsets.fromLTRB(16, 4, 16, 24),
      itemCount: pages.length,
      separatorBuilder: (_, _) => const SizedBox(height: 10),
      itemBuilder: (context, i) => _PageRow(page: pages[i]),
    );
  }
}

class _PageRow extends StatelessWidget {
  const _PageRow({required this.page});
  final BusinessPage page;

  @override
  Widget build(BuildContext context) {
    final avatar = page.avatarMediaId != null
        ? '${Environment.apiBaseUrl}/v1/media/${page.avatarMediaId}/serve'
        : null;
    return GestureDetector(
      onTap: () => context.push('/page/${page.pageHandle}'),
      child: Container(
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          color: AppColors.bgSecondary,
          borderRadius: BorderRadius.circular(16),
        ),
        child: Row(
          children: [
            CircleAvatar(
              radius: 26,
              backgroundColor: AppColors.bgTertiary,
              backgroundImage: avatar != null ? NetworkImage(avatar) : null,
              child: avatar == null
                  ? Text(page.pageName.isNotEmpty ? page.pageName[0].toUpperCase() : '?',
                      style: AppTextStyles.h3)
                  : null,
            ),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      Flexible(child: Text(page.pageName, maxLines: 1, overflow: TextOverflow.ellipsis, style: AppTextStyles.h3)),
                      if (page.isVerified) ...[
                        const SizedBox(width: 4),
                        const Icon(Icons.verified, size: 14, color: Color(0xFF2563EB)),
                      ],
                    ],
                  ),
                  const SizedBox(height: 2),
                  Text(
                    '${page.displayType ?? page.category} · ${page.followerCount} followers',
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: AppTextStyles.labelSmall.copyWith(color: AppColors.textTertiary),
                  ),
                ],
              ),
            ),
            const Icon(Icons.chevron_right, color: AppColors.textTertiary),
          ],
        ),
      ),
    );
  }
}

class _Loading extends StatelessWidget {
  const _Loading();
  @override
  Widget build(BuildContext context) =>
      const Center(child: CircularProgressIndicator(color: AppColors.postbookPrimary));
}

class _ErrorState extends StatelessWidget {
  const _ErrorState();
  @override
  Widget build(BuildContext context) =>
      Center(child: Text('Could not load pages.', style: AppTextStyles.body.copyWith(color: AppColors.textTertiary)));
}
