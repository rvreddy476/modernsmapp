/// Barrel for the commerce domain kernel.
///
/// NOTE: this package intentionally carries TWO order surfaces that
/// predate the extraction — `models/commerce.dart` and
/// `models/order.dart` each declare an `Order`, and `commerce_providers`
/// and `orders_provider` each declare an `orderDetailProvider`. They are
/// different types (commerce-checkout vs orders-history). To avoid a
/// silent wrong-type via this barrel, the barrel exposes the
/// orders-history `Order` / `orderDetailProvider`; import the specific
/// file (`package:commerce_domain/models/commerce.dart`) when you need
/// the commerce-checkout variant.
library;

export 'commerce_telemetry.dart';
export 'data/commerce_repository.dart';
export 'data/orders_repository.dart';
export 'data/product_tags_repository.dart';
export 'models/commerce.dart' hide Order, OrderItem;
export 'models/order.dart';
export 'providers/commerce_providers.dart' hide orderDetailProvider;
export 'providers/orders_provider.dart';
export 'providers/product_tags_provider.dart';
