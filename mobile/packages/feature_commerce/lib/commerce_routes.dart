import 'package:commerce_domain/models/commerce.dart' show Address;
import 'package:feature_commerce/commerce/address_book_screen.dart';
import 'package:feature_commerce/commerce/address_form_screen.dart';
import 'package:feature_commerce/commerce/affiliate_redirect_screen.dart';
import 'package:feature_commerce/commerce/cart_screen.dart';
import 'package:feature_commerce/commerce/checkout_screen.dart';
import 'package:feature_commerce/commerce/commerce_home_screen.dart';
import 'package:feature_commerce/commerce/commerce_order_detail_screen.dart';
import 'package:feature_commerce/commerce/my_orders_screen.dart';
import 'package:feature_commerce/commerce/my_returns_screen.dart';
import 'package:feature_commerce/commerce/product_detail_screen.dart';
import 'package:feature_commerce/commerce/product_reviews_screen.dart';
import 'package:feature_commerce/commerce/return_detail_screen.dart';
import 'package:feature_commerce/commerce/return_request_screen.dart';
import 'package:feature_commerce/commerce/rfq/rfq_detail_screen.dart';
import 'package:feature_commerce/commerce/rfq/rfq_list_screen.dart';
import 'package:feature_commerce/commerce/rfq/rfq_new_screen.dart';
import 'package:feature_commerce/commerce/search_screen.dart';
import 'package:feature_commerce/commerce/wishlist_screen.dart';
import 'package:feature_commerce/commerce/write_review_screen.dart';
import 'package:feature_commerce/orders/order_detail_screen.dart';
import 'package:feature_commerce/orders/orders_screen.dart';
import 'package:feature_commerce/seller/seller_bulk_import_screen.dart';
import 'package:feature_commerce/seller/seller_dashboard_screen.dart';
import 'package:feature_commerce/seller/seller_earnings_screen.dart';
import 'package:feature_commerce/seller/seller_orders_screen.dart';
import 'package:feature_commerce/seller/seller_products_screen.dart';
import 'package:feature_commerce/seller/seller_returns_screen.dart';
import 'package:feature_commerce/seller/seller_variants_screen.dart';
import 'package:go_router/go_router.dart';

/// Commerce route table (buyer + seller + RFQ + legacy /orders). The app
/// router spreads this into its shell.
///
/// Sprint 1 (commerce parity): the `/v1/commerce/*` surface lives at
/// `/commerce`. The legacy `/shop` route stays in the app until
/// shop-service callers are migrated (see COMMERCE_RECON §J).
List<RouteBase> commerceRoutes() => [
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
    builder: (context, state) => ProductDetailScreen(
      productId: state.pathParameters['id']!,
    ),
  ),
  GoRoute(
    path: '/commerce/product/:id/reviews',
    builder: (context, state) => ProductReviewsScreen(
      productId: state.pathParameters['id']!,
    ),
  ),
  GoRoute(
    path: '/commerce/cart',
    builder: (_, _) => const CartScreen(),
  ),
  // In-video affiliate redirect. Mirrors the public web URL
  // /v1/commerce/affiliate/:linkId; the screen calls the server endpoint
  // with redirects disabled, captures ?via= into AffiliateAttribution,
  // then routes onward.
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
    builder: (context, state) => ReturnRequestScreen(
      orderId: state.pathParameters['id']!,
    ),
  ),
  GoRoute(
    path: '/commerce/returns',
    builder: (_, _) => const MyReturnsScreen(),
  ),
  GoRoute(
    path: '/commerce/returns/:id',
    builder: (context, state) => ReturnDetailScreen(
      returnId: state.pathParameters['id']!,
    ),
  ),
  GoRoute(
    path: '/commerce/products/:id/review',
    builder: (context, state) {
      // The order detail screen passes seller_id + order_item_id +
      // product_title via `extra`. The backend requires them to mark the
      // review as a verified purchase.
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
    builder: (context, state) => SearchScreen(
      initialQuery: state.uri.queryParameters['q'],
    ),
  ),
  // Seller surface — dashboard + product management. The "My orders"
  // tile reuses the customer order list (sellers see their orders as a
  // buyer would, with fulfillment actions in a later slice).
  GoRoute(
    path: '/seller/dashboard',
    builder: (_, _) => const SellerDashboardScreen(),
  ),
  GoRoute(
    path: '/seller/products',
    builder: (_, _) => const SellerProductsScreen(),
  ),
  GoRoute(
    path: '/seller/products/:id/variants',
    builder: (context, state) => SellerVariantsScreen(
      productId: state.pathParameters['id']!,
    ),
  ),
  GoRoute(
    path: '/seller/orders',
    builder: (_, _) => const SellerOrdersScreen(),
  ),
  GoRoute(
    path: '/seller/returns',
    builder: (_, _) => const SellerReturnsScreen(),
  ),
  GoRoute(
    path: '/seller/earnings',
    builder: (_, _) => const SellerEarningsScreen(),
  ),
  GoRoute(
    path: '/seller/bulk-import',
    builder: (_, _) => const SellerBulkImportScreen(),
  ),
  // Phase F4 mobile — RFQ buyer flow.
  GoRoute(
    path: '/rfq',
    builder: (_, _) => const RFQListScreen(),
  ),
  GoRoute(
    path: '/rfq/new',
    builder: (context, state) {
      final sellerId = state.uri.queryParameters['seller_id'] ?? '';
      final variantId = state.uri.queryParameters['variant_id'] ?? '';
      return RFQNewScreen(
        sellerId: sellerId,
        variantId: variantId,
      );
    },
  ),
  GoRoute(
    path: '/rfq/:id',
    builder: (context, state) => RFQDetailScreen(
      rfqId: state.pathParameters['id']!,
    ),
  ),
  // Legacy generic order history (distinct from /commerce/orders — this
  // is the pre-commerce order list still linked from a few surfaces).
  GoRoute(
    path: '/orders',
    builder: (context, state) => const OrdersScreen(),
  ),
  GoRoute(
    path: '/orders/:orderId',
    builder: (context, state) =>
        OrderDetailScreen(orderId: state.pathParameters['orderId']!),
  ),
];
