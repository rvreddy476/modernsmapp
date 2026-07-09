import 'package:feature_groups/create_group_screen.dart';
import 'package:feature_groups/group_admin_screen.dart';
import 'package:feature_groups/group_detail_screen.dart';
import 'package:feature_groups/group_post_composer_screen.dart';
import 'package:feature_groups/groups_list_screen.dart';
import 'package:go_router/go_router.dart';

/// Groups ("MySpace") route table. Spread into the app router's shell.
List<RouteBase> groupsRoutes() => [
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
