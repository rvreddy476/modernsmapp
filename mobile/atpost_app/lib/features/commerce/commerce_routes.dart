import 'package:atpost_app/data/models/commerce.dart';
import 'package:atpost_app/features/commerce/address_book_screen.dart';
import 'package:atpost_app/features/commerce/address_form_screen.dart';
import 'package:atpost_app/features/commerce/affiliate_redirect_screen.dart';
import 'package:atpost_app/features/commerce/cart_screen.dart';
import 'package:atpost_app/features/commerce/checkout_screen.dart';
import 'package:atpost_app/features/commerce/commerce_home_screen.dart';
import 'package:atpost_app/features/commerce/commerce_order_detail_screen.dart';
import 'package:atpost_app/features/commerce/my_orders_screen.dart';
import 'package:atpost_app/features/commerce/my_returns_screen.dart';
import 'package:atpost_app/features/commerce/product_detail_screen.dart';
import 'package:atpost_app/features/commerce/product_reviews_screen.dart';
import 'package:atpost_app/features/commerce/return_detail_screen.dart';
import 'package:atpost_app/features/commerce/return_request_screen.dart';
import 'package:atpost_app/features/commerce/search_screen.dart';
import 'package:atpost_app/features/commerce/wishlist_screen.dart';
import 'package:atpost_app/features/commerce/write_review_screen.dart';
import 'package:atpost_app/features/commerce/rfq/rfq_detail_screen.dart';
import 'package:atpost_app/features/commerce/rfq/rfq_list_screen.dart';
import 'package:atpost_app/features/commerce/rfq/rfq_new_screen.dart';
import 'package:atpost_app/features/shop/shop_screen.dart';
import 'package:go_router/go_router.dart';

class CommerceRoutes {
  static List<RouteBase> get routes => [
        GoRoute(
          path: '/shop',
          builder: (context, state) => const ShopScreen(),
        ),
        GoRoute(
          path: '/commerce',
          builder: (_, _) => const CommerceHomeScreen(),
        ),
        GoRoute(
          path: '/commerce/category/:slug',
          builder: (context, state) => CommerceHomeScreen(
            initialCategorySlug: state.pathParameters['slug'],
          ),
        ),
        GoRoute(
          path: '/commerce/product/:id',
          builder: (context, state) =>
              ProductDetailScreen(productId: state.pathParameters['id']!),
        ),
        GoRoute(
          path: '/commerce/product/:id/reviews',
          builder: (context, state) =>
              ProductReviewsScreen(productId: state.pathParameters['id']!),
        ),
        GoRoute(
          path: '/commerce/cart',
          builder: (_, _) => const CartScreen(),
        ),
        GoRoute(
          path: '/commerce/affiliate/:linkId',
          builder: (context, state) => AffiliateRedirectScreen(
            linkId: state.pathParameters['linkId']!,
          ),
        ),
        GoRoute(
          path: '/commerce/checkout',
          builder: (_, _) => const CheckoutScreen(),
        ),
        GoRoute(
          path: '/commerce/addresses',
          builder: (context, state) => AddressBookScreen(
            pickerMode: state.uri.queryParameters['picker'] == '1',
          ),
        ),
        GoRoute(
          path: '/commerce/addresses/new',
          builder: (context, state) => AddressFormScreen(
            existing: state.extra is Address ? state.extra as Address : null,
          ),
        ),
        GoRoute(
          path: '/commerce/orders',
          builder: (_, _) => const MyOrdersScreen(),
        ),
        GoRoute(
          path: '/commerce/orders/:id',
          builder: (context, state) => CommerceOrderDetailScreen(
            orderId: state.pathParameters['id']!,
            justPlaced: state.uri.queryParameters['placed'] == '1',
          ),
        ),
        GoRoute(
          path: '/commerce/orders/:id/return',
          builder: (context, state) =>
              ReturnRequestScreen(orderId: state.pathParameters['id']!),
        ),
        GoRoute(
          path: '/commerce/returns',
          builder: (_, _) => const MyReturnsScreen(),
        ),
        GoRoute(
          path: '/commerce/returns/:id',
          builder: (context, state) =>
              ReturnDetailScreen(returnId: state.pathParameters['id']!),
        ),
        GoRoute(
          path: '/commerce/products/:id/review',
          builder: (context, state) {
            final extra = state.extra is Map
                ? Map<String, dynamic>.from(state.extra as Map)
                : <String, dynamic>{};
            return WriteReviewScreen(
              productId: state.pathParameters['id']!,
              sellerId: extra['seller_id']?.toString() ?? '',
              orderItemId: extra['order_item_id']?.toString() ?? '',
              productTitle: extra['product_title']?.toString(),
            );
          },
        ),
        GoRoute(
          path: '/commerce/wishlist',
          builder: (_, _) => const WishlistScreen(),
        ),
        GoRoute(
          path: '/commerce/search',
          builder: (context, state) =>
              SearchScreen(initialQuery: state.uri.queryParameters['q']),
        ),
        // RFQ
        GoRoute(path: '/rfq', builder: (_, _) => const RFQListScreen()),
        GoRoute(
          path: '/rfq/new',
          builder: (context, state) {
            final sellerId = state.uri.queryParameters['seller_id'] ?? '';
            final variantId = state.uri.queryParameters['variant_id'] ?? '';
            return RFQNewScreen(sellerId: sellerId, variantId: variantId);
          },
        ),
        GoRoute(
          path: '/rfq/:id',
          builder: (context, state) =>
              RFQDetailScreen(rfqId: state.pathParameters['id']!),
        ),
      ];
}
