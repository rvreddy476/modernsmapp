import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/broadcast_channel.dart';
import 'package:atpost_app/data/repositories/broadcast_channels_repository.dart';
import 'package:atpost_app/providers/broadcast_channels_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class ChannelsListScreen extends ConsumerWidget {
  const ChannelsListScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final repo = ref.read(broadcastChannelsRepositoryProvider);

    return DefaultTabController(
      length: 2,
      child: Scaffold(
        backgroundColor: AppColors.bgPrimary,
        appBar: AppBar(
          backgroundColor: AppColors.bgPrimary,
          elevation: 0,
          leading: IconButton(
            icon: const Icon(Icons.arrow_back, color: AppColors.textPrimary),
            onPressed: () => context.pop(),
          ),
          title: Text('Channels', style: AppTextStyles.h2),
          actions: [
            IconButton(
              icon: const Icon(Icons.add, color: AppColors.postbookPrimary),
              onPressed: () => context.push('/channels/create'),
            ),
          ],
          bottom: TabBar(
            labelColor: AppColors.postbookPrimary,
            unselectedLabelColor: AppColors.textDim,
            indicatorColor: AppColors.postbookPrimary,
            labelStyle: AppTextStyles.label,
            tabs: const [
              Tab(text: 'My Channels'),
              Tab(text: 'Discover'),
            ],
          ),
        ),
        body: TabBarView(
          children: [
            _ChannelsList(
              provider: myBroadcastChannelsProvider,
              repo: repo,
              emptyMessage: 'You have not subscribed to any channels yet.',
            ),
            _ChannelsList(
              provider: discoverBroadcastChannelsProvider,
              repo: repo,
              emptyMessage: 'No channels to discover.',
            ),
          ],
        ),
      ),
    );
  }
}

class _ChannelsList extends ConsumerWidget {
  final ProviderBase<AsyncValue<List<BroadcastChannel>>> provider;
  final BroadcastChannelsRepository repo;
  final String emptyMessage;

  const _ChannelsList({
    required this.provider,
    required this.repo,
    required this.emptyMessage,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final async = ref.watch(provider);
    return async.when(
      loading: () => const Center(
        child: CircularProgressIndicator(color: AppColors.postbookPrimary),
      ),
      error: (_, _) => Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.error_outline, color: AppColors.textDim, size: 40),
            const SizedBox(height: 12),
            Text('Failed to load channels', style: AppTextStyles.body),
            const SizedBox(height: 8),
            TextButton(
              onPressed: () => ref.invalidate(provider),
              child: Text('Retry',
                  style: AppTextStyles.label
                      .copyWith(color: AppColors.postbookPrimary)),
            ),
          ],
        ),
      ),
      data: (channels) {
        if (channels.isEmpty) {
          return Center(
            child: Text(emptyMessage,
                style:
                    AppTextStyles.body.copyWith(color: AppColors.textSecondary)),
          );
        }
        return ListView.separated(
          padding: AppSpacing.pagePadding.copyWith(top: 12, bottom: 100),
          itemCount: channels.length,
          separatorBuilder: (_, _) => const SizedBox(height: 8),
          itemBuilder: (context, index) =>
              _ChannelTile(channel: channels[index], repo: repo),
        );
      },
    );
  }
}

class _ChannelTile extends StatefulWidget {
  final BroadcastChannel channel;
  final BroadcastChannelsRepository repo;

  const _ChannelTile({required this.channel, required this.repo});

  @override
  State<_ChannelTile> createState() => _ChannelTileState();
}

class _ChannelTileState extends State<_ChannelTile> {
  late bool _subscribed;
  bool _loading = false;

  @override
  void initState() {
    super.initState();
    _subscribed = widget.channel.viewerRole != null;
  }

  Future<void> _toggle() async {
    if (_loading) return;
    final wasSubscribed = _subscribed;
    setState(() {
      _subscribed = !_subscribed;
      _loading = true;
    });
    try {
      if (wasSubscribed) {
        await widget.repo.unsubscribe(widget.channel.id);
      } else {
        await widget.repo.subscribe(widget.channel.id);
      }
    } catch (_) {
      if (mounted) {
        setState(() => _subscribed = wasSubscribed);
      }
    } finally {
      if (mounted) {
        setState(() => _loading = false);
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return Card(
      color: AppColors.bgCard,
      margin: EdgeInsets.zero,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
        side: const BorderSide(color: AppColors.borderSubtle),
      ),
      child: ListTile(
        contentPadding:
            const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
        leading: Container(
          width: 48,
          height: 48,
          decoration: BoxDecoration(
            gradient: const LinearGradient(
              colors: [AppColors.accentPurple, AppColors.postbookPrimary],
            ),
            borderRadius: BorderRadius.circular(12),
          ),
          child: widget.channel.avatarMediaId != null
              ? ClipRRect(
                  borderRadius: BorderRadius.circular(12),
                  child: Image.network(
                    '/v1/media/${widget.channel.avatarMediaId}/serve',
                    fit: BoxFit.cover,
                    errorBuilder: (_, _, _) =>
                        const Icon(Icons.campaign, color: Colors.white, size: 24),
                  ),
                )
              : Center(
                  child: Text(
                    widget.channel.name.isNotEmpty
                        ? widget.channel.name[0].toUpperCase()
                        : 'C',
                    style: const TextStyle(
                      color: Colors.white,
                      fontWeight: FontWeight.w900,
                      fontSize: 18,
                    ),
                  ),
                ),
        ),
        title: Row(
          children: [
            Flexible(
              child: Text(widget.channel.name, style: AppTextStyles.h3),
            ),
            if (widget.channel.isVerified) ...[
              const SizedBox(width: 4),
              const Icon(Icons.verified, color: Colors.blue, size: 16),
            ],
          ],
        ),
        subtitle: Text(
          '@${widget.channel.handle} · ${widget.channel.subscriberCount} subscribers',
          style: AppTextStyles.labelSmall
              .copyWith(color: AppColors.textSecondary),
        ),
        trailing: _loading
            ? const SizedBox(
                width: 20,
                height: 20,
                child: CircularProgressIndicator(
                  strokeWidth: 2,
                  color: AppColors.postbookPrimary,
                ),
              )
            : _subscribed
                ? OutlinedButton(
                    onPressed: _toggle,
                    style: OutlinedButton.styleFrom(
                      foregroundColor: AppColors.textSecondary,
                      side: const BorderSide(color: AppColors.borderSubtle),
                      padding: const EdgeInsets.symmetric(
                          horizontal: 12, vertical: 4),
                      minimumSize: Size.zero,
                      tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                      shape: RoundedRectangleBorder(
                        borderRadius: BorderRadius.circular(20),
                      ),
                    ),
                    child:
                        Text('Subscribed', style: AppTextStyles.labelSmall),
                  )
                : Container(
                    decoration: BoxDecoration(
                      gradient: AppColors.postbookGradient,
                      borderRadius: BorderRadius.circular(20),
                    ),
                    child: OutlinedButton(
                      onPressed: _toggle,
                      style: OutlinedButton.styleFrom(
                        foregroundColor: Colors.white,
                        side: BorderSide.none,
                        padding: const EdgeInsets.symmetric(
                            horizontal: 12, vertical: 4),
                        minimumSize: Size.zero,
                        tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                        shape: RoundedRectangleBorder(
                          borderRadius: BorderRadius.circular(20),
                        ),
                      ),
                      child:
                          Text('Subscribe', style: AppTextStyles.labelSmall),
                    ),
                  ),
        onTap: () => context.push('/channels/${widget.channel.id}'),
      ),
    );
  }
}
