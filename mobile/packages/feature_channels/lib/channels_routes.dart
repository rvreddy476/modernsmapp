import 'package:feature_channels/channel_detail_screen.dart';
import 'package:feature_channels/channels_list_screen.dart';
import 'package:feature_channels/create_channel_screen.dart';
import 'package:go_router/go_router.dart';

/// Broadcast-channels route table. Spread into the app router's shell.
List<RouteBase> channelsRoutes() => [
  GoRoute(path: '/channels', builder: (_, _) => const ChannelsListScreen()),
  GoRoute(
      path: '/channels/create',
      builder: (_, _) => const CreateChannelScreen()),
  GoRoute(
    path: '/channels/:channelId',
    builder: (context, state) =>
        ChannelDetailScreen(channelId: state.pathParameters['channelId']!),
  ),
];
