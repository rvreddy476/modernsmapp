import 'package:atpost_app/features/channels/channel_detail_screen.dart';
import 'package:atpost_app/features/channels/channels_list_screen.dart';
import 'package:atpost_app/features/channels/create_channel_screen.dart';
import 'package:atpost_app/features/groups/create_group_screen.dart';
import 'package:atpost_app/features/groups/group_admin_screen.dart';
import 'package:atpost_app/features/groups/group_detail_screen.dart';
import 'package:atpost_app/features/groups/group_post_composer_screen.dart';
import 'package:atpost_app/features/groups/groups_list_screen.dart';
import 'package:go_router/go_router.dart';

class CommunityRoutes {
  static List<RouteBase> get routes => [
        GoRoute(
          path: '/channels',
          builder: (context, state) => const ChannelsListScreen(),
        ),
        GoRoute(
          path: '/channels/create',
          builder: (context, state) => const CreateChannelScreen(),
        ),
        GoRoute(
          path: '/channels/:channelId',
          builder: (context, state) => ChannelDetailScreen(
            channelId: state.pathParameters['channelId']!,
          ),
        ),
        GoRoute(
          path: '/groups',
          builder: (context, state) => const GroupsListScreen(),
        ),
        GoRoute(
          path: '/groups/create',
          builder: (context, state) => const CreateGroupScreen(),
        ),
        GoRoute(
          path: '/groups/:groupId',
          builder: (context, state) =>
              GroupDetailScreen(groupId: state.pathParameters['groupId']!),
        ),
        GoRoute(
          path: '/groups/:groupId/post',
          builder: (context, state) => GroupPostComposerScreen(
            groupId: state.pathParameters['groupId']!,
          ),
        ),
        GoRoute(
          path: '/groups/:groupId/admin',
          builder: (context, state) =>
              GroupAdminScreen(groupId: state.pathParameters['groupId']!),
        ),
      ];
}
