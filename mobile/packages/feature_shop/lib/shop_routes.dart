import 'package:feature_shop/shop_screen.dart';
import 'package:go_router/go_router.dart';

/// Shop route table. Spread into the app router's shell.
List<RouteBase> shopRoutes() => [
  GoRoute(path: '/shop', builder: (_, _) => const ShopScreen()),
];
