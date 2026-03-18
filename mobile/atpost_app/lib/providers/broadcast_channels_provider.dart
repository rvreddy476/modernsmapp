import 'package:atpost_app/data/models/broadcast_channel.dart';
import 'package:atpost_app/data/repositories/broadcast_channels_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

final myBroadcastChannelsProvider =
    FutureProvider.autoDispose<List<BroadcastChannel>>((ref) async {
  return ref.watch(broadcastChannelsRepositoryProvider).getMyChannels();
});

final discoverBroadcastChannelsProvider =
    FutureProvider.autoDispose<List<BroadcastChannel>>((ref) async {
  return ref.watch(broadcastChannelsRepositoryProvider).discoverChannels();
});

final broadcastChannelDetailProvider = FutureProvider.autoDispose
    .family<BroadcastChannel, String>((ref, channelId) async {
  return ref.watch(broadcastChannelsRepositoryProvider).getChannel(channelId);
});

final channelUpdatesProvider = FutureProvider.autoDispose
    .family<List<ChannelUpdate>, String>((ref, channelId) async {
  return ref
      .watch(broadcastChannelsRepositoryProvider)
      .getUpdates(channelId);
});
