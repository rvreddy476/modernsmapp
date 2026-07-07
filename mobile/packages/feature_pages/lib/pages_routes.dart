import 'package:feature_pages/create_page_screen.dart';
import 'package:feature_pages/page_detail_screen.dart';
import 'package:feature_pages/pages_list_screen.dart';
import 'package:go_router/go_router.dart';

/// Business-pages route table. Spread into the app router's shell.
List<RouteBase> pagesRoutes() => [
  GoRoute(path: '/pages', builder: (_, _) => const PagesListScreen()),
  GoRoute(path: '/pages/create', builder: (_, _) => const CreatePageScreen()),
  GoRoute(
    path: '/page/:handle',
    builder: (context, state) =>
        PageDetailScreen(handle: state.pathParameters['handle'] ?? ''),
  ),
];
