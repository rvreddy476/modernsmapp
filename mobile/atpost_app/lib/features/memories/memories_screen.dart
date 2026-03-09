import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/memory.dart';
import 'package:atpost_app/data/repositories/memories_repository.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

final onThisDayProvider = FutureProvider.autoDispose<List<OnThisDayMemory>>((
  ref,
) async {
  final repo = ref.watch(memoriesRepositoryProvider);
  return repo.getOnThisDay();
});

final memoryCollectionsProvider =
    FutureProvider.autoDispose<List<MemoryCollection>>((ref) async {
      final repo = ref.watch(memoriesRepositoryProvider);
      return repo.getCollections();
    });

class MemoriesScreen extends ConsumerStatefulWidget {
  const MemoriesScreen({super.key});

  @override
  ConsumerState<MemoriesScreen> createState() => _MemoriesScreenState();
}

class _MemoriesScreenState extends ConsumerState<MemoriesScreen> {
  Future<void> _refresh() async {
    ref.invalidate(onThisDayProvider);
    ref.invalidate(memoryCollectionsProvider);
  }

  Future<void> _createCollection() async {
    final titleController = TextEditingController();
    final descriptionController = TextEditingController();

    String visibility = 'private';
    bool creating = false;

    final created = await showModalBottomSheet<bool>(
      context: context,
      isScrollControlled: true,
      backgroundColor: AppColors.bgSecondary,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      builder: (context) {
        return StatefulBuilder(
          builder: (context, setSheetState) {
            return SafeArea(
              top: false,
              child: Padding(
                padding: EdgeInsets.only(
                  left: 16,
                  right: 16,
                  top: 16,
                  bottom: MediaQuery.of(context).viewInsets.bottom + 16,
                ),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text('New Collection', style: AppTextStyles.h2),
                    const SizedBox(height: 12),
                    TextField(
                      controller: titleController,
                      maxLength: 60,
                      decoration: InputDecoration(
                        hintText: 'Collection title',
                        hintStyle: AppTextStyles.bodySmall,
                      ),
                    ),
                    const SizedBox(height: 8),
                    TextField(
                      controller: descriptionController,
                      minLines: 2,
                      maxLines: 3,
                      maxLength: 180,
                      decoration: InputDecoration(
                        hintText: 'Description (optional)',
                        hintStyle: AppTextStyles.bodySmall,
                      ),
                    ),
                    const SizedBox(height: 8),
                    Text('Visibility', style: AppTextStyles.label),
                    const SizedBox(height: 8),
                    Wrap(
                      spacing: 8,
                      children: ['private', 'public'].map((value) {
                        final selected = visibility == value;
                        return ChoiceChip(
                          label: Text(value),
                          selected: selected,
                          onSelected: (_) =>
                              setSheetState(() => visibility = value),
                          selectedColor: AppColors.postbookPrimary.withValues(
                            alpha: 0.2,
                          ),
                          backgroundColor: AppColors.bgCard,
                          side: BorderSide(
                            color: selected
                                ? AppColors.postbookPrimary
                                : AppColors.borderSubtle,
                          ),
                          labelStyle: AppTextStyles.label.copyWith(
                            color: selected
                                ? AppColors.postbookPrimary
                                : AppColors.textSecondary,
                          ),
                        );
                      }).toList(),
                    ),
                    const SizedBox(height: 14),
                    SizedBox(
                      width: double.infinity,
                      child: ElevatedButton.icon(
                        onPressed: creating
                            ? null
                            : () async {
                                final title = titleController.text.trim();
                                if (title.isEmpty) {
                                  ScaffoldMessenger.of(context).showSnackBar(
                                    const SnackBar(
                                      content: Text('Please add a title.'),
                                    ),
                                  );
                                  return;
                                }

                                setSheetState(() => creating = true);
                                try {
                                  await ref
                                      .read(memoriesRepositoryProvider)
                                      .createCollection(
                                        title: title,
                                        description: descriptionController.text
                                            .trim(),
                                        visibility: visibility,
                                      );
                                  if (!context.mounted) return;
                                  Navigator.of(context).pop(true);
                                } catch (_) {
                                  if (!context.mounted) return;
                                  ScaffoldMessenger.of(context).showSnackBar(
                                    const SnackBar(
                                      content: Text(
                                        'Could not create collection.',
                                      ),
                                    ),
                                  );
                                } finally {
                                  if (context.mounted) {
                                    setSheetState(() => creating = false);
                                  }
                                }
                              },
                        style: ElevatedButton.styleFrom(
                          backgroundColor: AppColors.postbookPrimary,
                          foregroundColor: Colors.white,
                          shape: RoundedRectangleBorder(
                            borderRadius: BorderRadius.circular(12),
                          ),
                          padding: const EdgeInsets.symmetric(vertical: 14),
                        ),
                        icon: creating
                            ? const SizedBox(
                                width: 14,
                                height: 14,
                                child: CircularProgressIndicator(
                                  strokeWidth: 2,
                                  color: Colors.white,
                                ),
                              )
                            : const Icon(Icons.add_circle_outline),
                        label: const Text('Create Collection'),
                      ),
                    ),
                  ],
                ),
              ),
            );
          },
        );
      },
    );

    titleController.dispose();
    descriptionController.dispose();

    if (created == true) {
      ref.invalidate(memoryCollectionsProvider);
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(const SnackBar(content: Text('Collection created.')));
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final memoriesAsync = ref.watch(onThisDayProvider);
    final collectionsAsync = ref.watch(memoryCollectionsProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: SafeArea(
        child: RefreshIndicator(
          color: AppColors.postbookPrimary,
          onRefresh: _refresh,
          child: CustomScrollView(
            physics: const AlwaysScrollableScrollPhysics(),
            slivers: [
              SliverToBoxAdapter(
                child: Padding(
                  padding: AppSpacing.pagePadding.copyWith(
                    top: 12,
                    bottom: 110,
                  ),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      _MemoriesHero(
                        onRefresh: _refresh,
                        onCreateCollection: _createCollection,
                      ),
                      const SizedBox(height: 20),
                      Row(
                        children: [
                          Text('On This Day', style: AppTextStyles.h2),
                          const Spacer(),
                          TextButton(
                            onPressed: () => context.push('/create'),
                            child: Text(
                              'Create post',
                              style: AppTextStyles.label.copyWith(
                                color: AppColors.postbookPrimary,
                              ),
                            ),
                          ),
                        ],
                      ),
                      const SizedBox(height: 10),
                      memoriesAsync.when(
                        data: (memories) {
                          if (memories.isEmpty) {
                            return const _InlineStateCard(
                              icon: Icons.calendar_today_outlined,
                              message: 'No memories for today yet.',
                            );
                          }

                          return SizedBox(
                            height: 220,
                            child: ListView.separated(
                              scrollDirection: Axis.horizontal,
                              itemCount: memories.length,
                              separatorBuilder: (_, _) =>
                                  const SizedBox(width: 10),
                              itemBuilder: (context, index) {
                                final memory = memories[index];
                                return _MemoryCard(
                                  memory: memory,
                                  onTap: () => context.push(
                                    '/comments/${memory.postId}',
                                  ),
                                );
                              },
                            ),
                          );
                        },
                        loading: () => const SizedBox(
                          height: 220,
                          child: Center(
                            child: CircularProgressIndicator(
                              color: AppColors.postbookPrimary,
                            ),
                          ),
                        ),
                        error: (_, _) => const _InlineStateCard(
                          icon: Icons.photo_album_outlined,
                          message: 'Could not load on-this-day memories.',
                        ),
                      ),
                      const SizedBox(height: 24),
                      Row(
                        children: [
                          Text('Collections', style: AppTextStyles.h2),
                          const Spacer(),
                          ElevatedButton.icon(
                            onPressed: _createCollection,
                            style: ElevatedButton.styleFrom(
                              backgroundColor: AppColors.postbookPrimary,
                              foregroundColor: Colors.white,
                              shape: RoundedRectangleBorder(
                                borderRadius: BorderRadius.circular(10),
                              ),
                            ),
                            icon: const Icon(Icons.add, size: 16),
                            label: const Text('New'),
                          ),
                        ],
                      ),
                      const SizedBox(height: 10),
                      collectionsAsync.when(
                        data: (collections) {
                          if (collections.isEmpty) {
                            return const _InlineStateCard(
                              icon: Icons.collections_bookmark_outlined,
                              message:
                                  'Create a collection to organize your memories.',
                            );
                          }

                          return LayoutBuilder(
                            builder: (context, constraints) {
                              final width = constraints.maxWidth;
                              final crossAxisCount = width >= 800
                                  ? 3
                                  : width >= 520
                                  ? 2
                                  : 1;

                              return GridView.builder(
                                shrinkWrap: true,
                                physics: const NeverScrollableScrollPhysics(),
                                gridDelegate:
                                    SliverGridDelegateWithFixedCrossAxisCount(
                                      crossAxisCount: crossAxisCount,
                                      childAspectRatio: 1.35,
                                      crossAxisSpacing: 10,
                                      mainAxisSpacing: 10,
                                    ),
                                itemCount: collections.length,
                                itemBuilder: (context, index) {
                                  final collection = collections[index];
                                  return _CollectionCard(
                                    collection: collection,
                                  );
                                },
                              );
                            },
                          );
                        },
                        loading: () => const Padding(
                          padding: EdgeInsets.symmetric(vertical: 20),
                          child: Center(
                            child: CircularProgressIndicator(
                              color: AppColors.postbookPrimary,
                            ),
                          ),
                        ),
                        error: (_, _) => const _InlineStateCard(
                          icon: Icons.collections_bookmark_outlined,
                          message: 'Could not load collections.',
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _MemoriesHero extends StatelessWidget {
  const _MemoriesHero({
    required this.onRefresh,
    required this.onCreateCollection,
  });

  final VoidCallback onRefresh;
  final VoidCallback onCreateCollection;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        gradient: const LinearGradient(
          colors: [Color(0x337B68EE), Color(0x334ECDC4), Color(0x33FF6B35)],
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
        ),
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        border: Border.all(color: AppColors.borderMedium),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  'Memories',
                  style: AppTextStyles.h1.copyWith(fontSize: 30),
                ),
              ),
              IconButton(
                onPressed: onRefresh,
                icon: const Icon(
                  Icons.refresh_rounded,
                  color: AppColors.textPrimary,
                ),
              ),
            ],
          ),
          Text(
            'Revisit your old moments and group your highlights.',
            style: AppTextStyles.bodySmall,
          ),
          const SizedBox(height: 12),
          Row(
            children: [
              const _MiniChip(icon: Icons.calendar_today, text: 'On this day'),
              const SizedBox(width: 8),
              const _MiniChip(icon: Icons.collections, text: 'Collections'),
              const Spacer(),
              OutlinedButton.icon(
                onPressed: onCreateCollection,
                style: OutlinedButton.styleFrom(
                  foregroundColor: AppColors.textPrimary,
                  side: const BorderSide(color: AppColors.borderSubtle),
                ),
                icon: const Icon(Icons.add, size: 16),
                label: const Text('New'),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

class _MiniChip extends StatelessWidget {
  const _MiniChip({required this.icon, required this.text});

  final IconData icon;
  final String text;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 6),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(999),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        children: [
          Icon(icon, size: 14, color: AppColors.textSecondary),
          const SizedBox(width: 4),
          Text(text, style: AppTextStyles.labelSmall),
        ],
      ),
    );
  }
}

class _InlineStateCard extends StatelessWidget {
  const _InlineStateCard({required this.icon, required this.message});

  final IconData icon;
  final String message;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        children: [
          Icon(icon, color: AppColors.textMuted),
          const SizedBox(width: 10),
          Expanded(child: Text(message, style: AppTextStyles.bodySmall)),
        ],
      ),
    );
  }
}

class _MemoryCard extends StatelessWidget {
  const _MemoryCard({required this.memory, required this.onTap});

  final OnThisDayMemory memory;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: 180,
      child: Material(
        color: Colors.transparent,
        child: InkWell(
          onTap: onTap,
          borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
          child: Ink(
            decoration: BoxDecoration(
              color: AppColors.bgCard,
              borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
              border: Border.all(color: AppColors.borderSubtle),
            ),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Expanded(
                  child: Container(
                    width: double.infinity,
                    decoration: BoxDecoration(
                      borderRadius: const BorderRadius.vertical(
                        top: Radius.circular(16),
                      ),
                      gradient: const LinearGradient(
                        colors: [Color(0x3325B2FF), Color(0x337B68EE)],
                      ),
                    ),
                    child: Stack(
                      children: [
                        if ((memory.mediaUrl ?? '').isNotEmpty)
                          ClipRRect(
                            borderRadius: const BorderRadius.vertical(
                              top: Radius.circular(16),
                            ),
                            child: Image.network(
                              memory.mediaUrl!,
                              fit: BoxFit.cover,
                              width: double.infinity,
                              height: double.infinity,
                              errorBuilder: (_, _, _) =>
                                  const SizedBox.shrink(),
                            ),
                          )
                        else
                          const Center(
                            child: Icon(
                              Icons.photo_size_select_actual_outlined,
                              color: AppColors.textSecondary,
                              size: 36,
                            ),
                          ),
                        Positioned(
                          top: 8,
                          right: 8,
                          child: Container(
                            padding: const EdgeInsets.symmetric(
                              horizontal: 8,
                              vertical: 4,
                            ),
                            decoration: BoxDecoration(
                              color: Colors.black.withValues(alpha: 0.4),
                              borderRadius: BorderRadius.circular(999),
                            ),
                            child: Text(
                              '${memory.yearsAgo}y',
                              style: AppTextStyles.labelSmall.copyWith(
                                color: Colors.white,
                              ),
                            ),
                          ),
                        ),
                      ],
                    ),
                  ),
                ),
                Padding(
                  padding: const EdgeInsets.all(10),
                  child: Text(
                    memory.snippet.isEmpty ? 'View memory' : memory.snippet,
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                    style: AppTextStyles.label,
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class _CollectionCard extends StatelessWidget {
  const _CollectionCard({required this.collection});

  final MemoryCollection collection;

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Expanded(
            child: Container(
              width: double.infinity,
              decoration: BoxDecoration(
                borderRadius: const BorderRadius.vertical(
                  top: Radius.circular(16),
                ),
                gradient: const LinearGradient(
                  colors: [Color(0x334ECDC4), Color(0x33FF6B35)],
                ),
              ),
              child: (collection.coverUrl ?? '').isNotEmpty
                  ? ClipRRect(
                      borderRadius: const BorderRadius.vertical(
                        top: Radius.circular(16),
                      ),
                      child: Image.network(
                        collection.coverUrl!,
                        fit: BoxFit.cover,
                        width: double.infinity,
                        errorBuilder: (_, _, _) => const SizedBox.shrink(),
                      ),
                    )
                  : const Center(
                      child: Icon(
                        Icons.collections_bookmark_outlined,
                        color: AppColors.textSecondary,
                        size: 34,
                      ),
                    ),
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
                  style: AppTextStyles.h3,
                ),
                const SizedBox(height: 2),
                Text(
                  '${collection.itemCount} item${collection.itemCount == 1 ? '' : 's'}  |  ${collection.visibility}',
                  style: AppTextStyles.labelSmall,
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}
