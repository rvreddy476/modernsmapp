import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/memory.dart';
import 'package:atpost_app/data/repositories/memories_repository.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

final onThisDayProvider = FutureProvider.autoDispose<List<OnThisDayMemory>>((ref) async {
  final repo = ref.watch(memoriesRepositoryProvider);
  return repo.getOnThisDay();
});

final memoryCollectionsProvider = FutureProvider.autoDispose<List<MemoryCollection>>((ref) async {
  final repo = ref.watch(memoriesRepositoryProvider);
  return repo.getCollections();
});

class MemoriesScreen extends ConsumerWidget {
  const MemoriesScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final memoriesAsync = ref.watch(onThisDayProvider);
    final collectionsAsync = ref.watch(memoryCollectionsProvider);

    return SafeArea(
      child: SingleChildScrollView(
        padding: AppSpacing.pagePadding.copyWith(top: 16, bottom: 100),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                Text('Memories', style: AppTextStyles.h1),
                IconButton(
                  icon: const Icon(Icons.settings_outlined, color: AppColors.textSecondary),
                  onPressed: () {},
                ),
              ],
            ),
            const SizedBox(height: 20),

            // On This Day section
            _SectionHeader(title: 'On This Day', icon: Icons.calendar_today),
            const SizedBox(height: 12),
            memoriesAsync.when(
              data: (memories) {
                if (memories.isEmpty) {
                  return _EmptyCard(
                    icon: Icons.photo_album_outlined,
                    message: 'No memories for today. Keep posting!',
                  );
                }
                return SizedBox(
                  height: 200,
                  child: ListView.separated(
                    scrollDirection: Axis.horizontal,
                    itemCount: memories.length,
                    separatorBuilder: (_, _) => const SizedBox(width: 12),
                    itemBuilder: (context, index) => _MemoryCard(memory: memories[index]),
                  ),
                );
              },
              loading: () => const SizedBox(
                height: 200,
                child: Center(child: CircularProgressIndicator()),
              ),
              error: (_, _) => _EmptyCard(
                icon: Icons.photo_album_outlined,
                message: 'No memories for today yet',
              ),
            ),
            const SizedBox(height: 28),

            // Collections section
            Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                _SectionHeader(title: 'Collections', icon: Icons.collections_bookmark),
                TextButton.icon(
                  onPressed: () {},
                  icon: const Icon(Icons.add, size: 18),
                  label: const Text('New'),
                ),
              ],
            ),
            const SizedBox(height: 12),
            collectionsAsync.when(
              data: (collections) {
                if (collections.isEmpty) {
                  return _EmptyCard(
                    icon: Icons.collections_bookmark_outlined,
                    message: 'Create a collection to organize your memories',
                  );
                }
                return GridView.builder(
                  shrinkWrap: true,
                  physics: const NeverScrollableScrollPhysics(),
                  gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(
                    crossAxisCount: 2,
                    childAspectRatio: 1.2,
                    crossAxisSpacing: 12,
                    mainAxisSpacing: 12,
                  ),
                  itemCount: collections.length,
                  itemBuilder: (context, index) => _CollectionCard(collection: collections[index]),
                );
              },
              loading: () => const Center(child: CircularProgressIndicator()),
              error: (_, _) => _EmptyCard(
                icon: Icons.collections_bookmark_outlined,
                message: 'Create a collection to organize your memories',
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _SectionHeader extends StatelessWidget {
  final String title;
  final IconData icon;

  const _SectionHeader({required this.title, required this.icon});

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        Icon(icon, size: 20, color: AppColors.postbookPrimary),
        const SizedBox(width: 8),
        Text(title, style: AppTextStyles.h2),
      ],
    );
  }
}

class _EmptyCard extends StatelessWidget {
  final IconData icon;
  final String message;

  const _EmptyCard({required this.icon, required this.message});

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.symmetric(vertical: 32, horizontal: 20),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, color: AppColors.textDim, size: 40),
          const SizedBox(height: 8),
          Text(message, style: AppTextStyles.body.copyWith(color: AppColors.textDim), textAlign: TextAlign.center),
        ],
      ),
    );
  }
}

class _MemoryCard extends StatelessWidget {
  final OnThisDayMemory memory;

  const _MemoryCard({required this.memory});

  @override
  Widget build(BuildContext context) {
    return Container(
      width: 160,
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // Image/media area
          Expanded(
            child: Container(
              decoration: BoxDecoration(
                color: AppColors.postbookPrimary.withValues(alpha: 0.1),
                borderRadius: const BorderRadius.vertical(top: Radius.circular(16)),
              ),
              child: Center(
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Text(
                      '${memory.yearsAgo}',
                      style: AppTextStyles.h1.copyWith(color: AppColors.postbookPrimary, fontSize: 32),
                    ),
                    Text(
                      memory.yearsAgo == 1 ? 'year ago' : 'years ago',
                      style: AppTextStyles.labelSmall.copyWith(color: AppColors.postbookPrimary),
                    ),
                  ],
                ),
              ),
            ),
          ),
          // Snippet
          Padding(
            padding: const EdgeInsets.all(10),
            child: Text(
              memory.snippet.isNotEmpty ? memory.snippet : 'View memory',
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
              style: AppTextStyles.labelSmall,
            ),
          ),
        ],
      ),
    );
  }
}

class _CollectionCard extends StatelessWidget {
  final MemoryCollection collection;

  const _CollectionCard({required this.collection});

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Expanded(
            child: Container(
              decoration: BoxDecoration(
                color: AppColors.bgPrimary,
                borderRadius: const BorderRadius.vertical(top: Radius.circular(16)),
              ),
              child: collection.coverUrl != null
                  ? ClipRRect(
                      borderRadius: const BorderRadius.vertical(top: Radius.circular(16)),
                      child: Image.network(collection.coverUrl!, fit: BoxFit.cover, width: double.infinity),
                    )
                  : const Center(child: Icon(Icons.photo_library_outlined, color: AppColors.textDim, size: 32)),
            ),
          ),
          Padding(
            padding: const EdgeInsets.all(10),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  collection.title,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: AppTextStyles.body.copyWith(fontWeight: FontWeight.w600, fontSize: 13),
                ),
                Text(
                  '${collection.itemCount} items',
                  style: AppTextStyles.labelSmall.copyWith(color: AppColors.textDim),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}
