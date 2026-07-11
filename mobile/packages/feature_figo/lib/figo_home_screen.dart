import 'dart:async';

import 'package:atpost_design/app_images.dart';
import 'package:atpost_design/app_colors.dart';
import 'package:atpost_design/app_spacing.dart';
import 'package:atpost_design/app_text_styles.dart';
import 'package:feature_figo/providers/figo_realtime_provider.dart';
import 'package:atpost_network/api_client.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:geolocator/geolocator.dart';

enum _FigoRole { customer, restaurant, delivery, admin }

class FigoHomeScreen extends ConsumerStatefulWidget {
  const FigoHomeScreen({super.key});

  @override
  ConsumerState<FigoHomeScreen> createState() => _FigoHomeScreenState();
}

class _FigoHomeScreenState extends ConsumerState<FigoHomeScreen> {
  late Future<_FigoSnapshot> _snapshotFuture;
  _FigoRole _role = _FigoRole.customer;
  String _paymentMethod = 'COD';

  /// Active cuisine chip; filters the nearby-restaurant list client-side.
  String? _cuisineFilter;

  @override
  void initState() {
    super.initState();
    _snapshotFuture = _load();
  }

  Future<_FigoSnapshot> _load() async {
    final api = ref.read(apiClientProvider);
    // Customer-facing data — always loaded, and fetched IN PARALLEL:
    // the previous sequential awaits stacked 4-6 round-trips into a
    // waterfall before first paint.
    final results = await Future.wait([
      _safeLoad<_FigoHome>(() async {
        final data = _responseData((await api.get('/v1/food/home')).data);
        return _FigoHome.fromJson(data);
      }, fallback: const _FigoHome(cuisines: [], restaurants: [])),
      _safeLoad<_FigoCart?>(() async {
        final data = _responseData((await api.get('/v1/food/cart')).data);
        return _FigoCart.fromJson(data);
      }, fallback: null),
      _safeLoad<List<_FigoOrder>>(() async {
        final data = _responseData((await api.get('/v1/food/orders')).data);
        return _items(data).map(_FigoOrder.fromJson).toList();
      }, fallback: const <_FigoOrder>[]),
      _safeLoad<List<_FigoAddress>>(() async {
        final data = _responseData((await api.get('/v1/food/addresses')).data);
        return _items(data).map(_FigoAddress.fromJson).toList();
      }, fallback: const <_FigoAddress>[]),
    ]);
    final home = results[0] as _FigoHome;
    final cart = results[1] as _FigoCart?;
    final orders = results[2] as List<_FigoOrder>;
    final addresses = results[3] as List<_FigoAddress>;
    const double walletBalance = 0;

    // Tracking + batch depend on the first order id — fetch them in
    // parallel with each other once orders are known.
    final trackingAndBatch = await Future.wait([
      _safeLoad<_OrderTracking?>(() async {
        if (orders.isEmpty) return null;
        final data = _responseData(
          (await api.get('/v1/food/orders/${orders.first.id}/tracking')).data,
        );
        return _OrderTracking.fromJson(data);
      }, fallback: null),
      // P2 batching: when the tracked order is part of a multi-pickup
      // batch, the customer banner says "delivered alongside N orders".
      // 404 → null; we render nothing for solo orders.
      _safeLoad<_OrderBatch?>(() async {
        if (orders.isEmpty) return null;
        final data = _responseData(
          (await api.get(
            '/v1/food/delivery/orders/${orders.first.id}/batch',
          )).data,
        );
        return _OrderBatch.fromJson(data, orders.first.id);
      }, fallback: null),
    ]);
    final tracking = trackingAndBatch[0] as _OrderTracking?;
    final batch = trackingAndBatch[1] as _OrderBatch?;

    // Role-gated data — only fetched when the matching workspace is
    // open. P0.5: a customer must never hit /v1/food/admin/* or
    // /v1/food/partner/* or /v1/food/delivery/*. Those endpoints are
    // internal-key-gated server-side and were producing 403s under a
    // _safeLoad swallow; worse, they were leaking the customer's
    // X-User-Id at endpoints they have no business calling.
    final partnerRestaurants = _role != _FigoRole.restaurant
        ? const <_PartnerRestaurant>[]
        : await _safeLoad<List<_PartnerRestaurant>>(
            () async {
              final data = _responseData(
                (await api.get('/v1/food/partner/restaurants')).data,
              );
              return _items(data).map(_PartnerRestaurant.fromJson).toList();
            },
            fallback: const [],
          );
    final delivery = _role != _FigoRole.delivery
        ? const _DeliveryWorkspace()
        : await _safeLoad<_DeliveryWorkspace>(() async {
            final profileData = _responseData(
              (await api.get('/v1/food/delivery/profile')).data,
            );
            final assignmentData = _responseData(
              (await api.get('/v1/food/delivery/assignments/current')).data,
            );
            final earningsData = _responseData(
              (await api.get('/v1/food/delivery/earnings')).data,
            );
            final assignment = _DeliveryAssignment.fromJson(assignmentData);
            final trackingData = await _safeLoad<_AssignmentTracking?>(
              () async {
                final data = _responseData(
                  (await api.get(
                    '/v1/food/delivery/assignments/${assignment.id}/tracking',
                  )).data,
                );
                return _AssignmentTracking.fromJson(data);
              },
              fallback: null,
            );
            return _DeliveryWorkspace(
              profile: _DeliveryProfile.fromJson(profileData),
              currentAssignment: assignment,
              earnings: _DeliveryEarnings.fromJson(earningsData),
              tracking: trackingData,
            );
          }, fallback: const _DeliveryWorkspace());
    final admin = _role != _FigoRole.admin
        ? const _AdminWorkspace()
        : await _safeLoad<_AdminWorkspace>(() async {
            final dashboardData = _responseData(
              (await api.get('/v1/food/admin/dashboard')).data,
            );
            final ordersData = _responseData(
              (await api.get('/v1/food/admin/orders')).data,
            );
            final settlementsData = _responseData(
              (await api.get(
                '/v1/food/admin/settlements/delivery-partners',
              )).data,
            );
            final restaurantSettlementsData = _responseData(
              (await api.get('/v1/food/admin/settlements/restaurants')).data,
            );
            final auditData = _responseData(
              (await api.get('/v1/food/admin/audit-logs')).data,
            );
            return _AdminWorkspace(
              dashboard: _AdminDashboard.fromJson(dashboardData),
              orders: _items(ordersData).map(_FigoOrder.fromJson).toList(),
              deliverySettlements: _items(settlementsData).length,
              restaurantSettlements: _items(restaurantSettlementsData).length,
              auditLogs: _items(auditData).length,
            );
          }, fallback: const _AdminWorkspace());
    return _FigoSnapshot(
      home: home,
      cart: cart,
      orders: orders,
      addresses: addresses,
      walletBalance: walletBalance,
      tracking: tracking,
      batch: batch,
      partnerRestaurants: partnerRestaurants,
      delivery: delivery,
      admin: admin,
    );
  }

  Future<T> _safeLoad<T>(
    Future<T> Function() loader, {
    required T fallback,
  }) async {
    try {
      return await loader();
    } catch (_) {
      return fallback;
    }
  }

  // Idempotency keys are minted once per user intent and reused across
  // retries. The old pattern regenerated the key on every attempt, so a
  // timed-out request followed by a user retry looked like two distinct
  // orders/payments to the server (double-order / double-charge risk).
  // A key is dropped only after the server acknowledges the intent.
  final Map<String, String> _idemKeys = {};

  Options _idempotent(String intent) => Options(
        headers: {
          'Idempotency-Key': _idemKeys.putIfAbsent(
            intent,
            () => '$intent-${DateTime.now().microsecondsSinceEpoch}'
                '-${identityHashCode(Object())}',
          ),
        },
      );

  void _idemDone(String intent) => _idemKeys.remove(intent);

  void _retry() {
    setState(() {
      _snapshotFuture = _load();
    });
  }

  Future<void> _runAction(Future<void> Function() action) async {
    try {
      await action();
      if (!mounted) return;
      _retry();
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('FiGo updated')));
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(SnackBar(content: Text(e.toString())));
    }
  }

  Future<void> _addFirstRecommendedItem(_Restaurant restaurant) async {
    final api = ref.read(apiClientProvider);
    final menuData = _responseData(
      (await api.get('/v1/food/restaurants/${restaurant.id}/menu')).data,
    );
    final categories = _itemsFromKey(menuData, 'categories');
    for (final category in categories) {
      for (final item in _itemsFromKey(category, 'items')) {
        if (item['is_available'] as bool? ?? false) {
          await api.post(
            '/v1/food/cart/items',
            data: {
              'menu_item_id': item['id'],
              'quantity': 1,
              'clear_existing': true,
            },
          );
          return;
        }
      }
    }
    throw Exception('No available items in this restaurant.');
  }

  Future<void> _deliveryAssignmentAction(
    _DeliveryAssignment assignment,
    String action,
  ) async {
    final intent = 'delivery-${assignment.id}-$action';
    await ref.read(apiClientProvider).post(
          '/v1/food/delivery/assignments/${assignment.id}/$action',
          options: _idempotent(intent),
        );
    _idemDone(intent);
  }

  Future<void> _partnerOrderAction(_FigoOrder order, String action) async {
    final intent = 'partner-${order.id}-$action';
    await ref.read(apiClientProvider).post(
          '/v1/food/partner/orders/${order.id}/$action',
          options: _idempotent(intent),
        );
    _idemDone(intent);
  }

  Future<void> _cancelOrder(_FigoOrder order) async {
    final intent = 'cancel-${order.id}';
    await ref.read(apiClientProvider).post(
          '/v1/food/orders/${order.id}/cancel',
          data: {'reason': 'Cancelled by customer from FiGo mobile'},
          options: _idempotent(intent),
        );
    _idemDone(intent);
  }

  Future<void> _rateOrder(_FigoOrder order, String target) async {
    await ref
        .read(apiClientProvider)
        .post(
          '/v1/food/orders/${order.id}/ratings/$target',
          data: const {'rating': 5, 'review': 'Rated from FiGo mobile'},
        );
  }

  Future<void> _placeOrder(_FigoAddress address, String paymentMethod) async {
    final api = ref.read(apiClientProvider);
    // One key per checkout attempt: a timeout + retry must replay THIS
    // order, not create a second one.
    const placeIntent = 'place-order';
    final response = await api.post(
      '/v1/food/orders',
      data: {
        'address_id': address.id,
        'payment_method': paymentMethod,
        'customer_instruction': 'Placed from FiGo mobile',
      },
      options: _idempotent(placeIntent),
    );
    _idemDone(placeIntent);
    final order = _FigoOrder.fromJson(_responseData(response.data));
    if (paymentMethod != 'COD') {
      final payIntent = 'pay-${order.id}';
      final intent = _responseData(
        (await api.post(
          '/v1/food/orders/${order.id}/payments/intents',
          data: {'method': paymentMethod},
          options: _idempotent(payIntent),
        )).data,
      );
      _idemDone(payIntent);
      if (paymentMethod == 'WALLET') {
        await api.post(
          '/v1/food/orders/${order.id}/payments/confirm',
          data: {
            'provider_reference': intent['provider_order_id']?.toString() ?? '',
          },
        );
      }
    }
  }

  /// Creates a delivery address (POST /v1/food/addresses). Without this
  /// a new user has no way to add an address and can never check out.
  Future<void> _createAddress(Map<String, dynamic> body) async {
    await ref.read(apiClientProvider).post('/v1/food/addresses', data: body);
  }

  Future<void> _showSearchSheet() async {
    await showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      backgroundColor: AppColors.bgSecondary,
      builder: (context) => const _FigoSearchSheet(),
    );
  }

  Future<void> _showAddAddressSheet() async {
    final body = await showModalBottomSheet<Map<String, dynamic>>(
      context: context,
      isScrollControlled: true,
      backgroundColor: AppColors.bgSecondary,
      builder: (context) => const _AddAddressSheet(),
    );
    if (body == null || !mounted) return;
    await _runAction(() => _createAddress(body));
  }

  Future<void> _updateDeliveryLocation() async {
    final enabled = await Geolocator.isLocationServiceEnabled();
    if (!enabled) {
      throw Exception('Location services are disabled.');
    }
    var permission = await Geolocator.checkPermission();
    if (permission == LocationPermission.denied) {
      permission = await Geolocator.requestPermission();
    }
    if (permission == LocationPermission.denied ||
        permission == LocationPermission.deniedForever) {
      throw Exception('Location permission denied.');
    }
    final position = await Geolocator.getCurrentPosition(
      desiredAccuracy: LocationAccuracy.high,
    );
    await ref
        .read(apiClientProvider)
        .post(
          '/v1/food/delivery/location',
          data: {
            'latitude': position.latitude,
            'longitude': position.longitude,
            'accuracy_meters': position.accuracy,
          },
        );
  }

  Future<void> _adminOrderAction(_FigoOrder order, String action) async {
    final intent = 'admin-${order.id}-$action';
    await ref.read(apiClientProvider).post(
          '/v1/food/admin/orders/${order.id}/$action',
          data: {'reason': 'Updated from mobile FiGo console'},
          options: action == 'refund' ? _idempotent(intent) : null,
        );
    if (action == 'refund') _idemDone(intent);
  }

  Future<void> _generateSettlements() async {
    final now = DateTime.now();
    final start = now.subtract(const Duration(days: 7));
    String fmt(DateTime date) =>
        '${date.year.toString().padLeft(4, '0')}-${date.month.toString().padLeft(2, '0')}-${date.day.toString().padLeft(2, '0')}';
    // Settlement generation is idempotent per period window, not per tap.
    final intent = 'settle-${fmt(start)}-${fmt(now)}';
    await ref.read(apiClientProvider).post(
          '/v1/food/admin/settlements/generate',
          data: {'period_start': fmt(start), 'period_end': fmt(now)},
          options: _idempotent(intent),
        );
    _idemDone(intent);
  }

  @override
  Widget build(BuildContext context) {
    // G2: SSE push — refresh the snapshot whenever a food.order.*
    // event lands on the realtime gateway. The REST snapshot still
    // owns the UI's data; this listener just stops the 0-5s polling
    // lag on status changes / substitutions / refund updates.
    ref.listen(foodOrderPushProvider, (prev, next) {
      next.whenData((_) => _retry());
    });

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('FiGo', style: AppTextStyles.h2),
      ),
      body: FutureBuilder<_FigoSnapshot>(
        future: _snapshotFuture,
        builder: (context, snapshot) {
          if (snapshot.connectionState == ConnectionState.waiting) {
            return const _FigoSkeleton();
          }
          if (snapshot.hasError) {
            return _FigoError(onRetry: _retry);
          }
          final data = snapshot.data ?? _FigoSnapshot.empty();
          return RefreshIndicator(
            onRefresh: () async => _retry(),
            child: ListView(
              padding: const EdgeInsets.fromLTRB(18, 8, 18, 32),
              children: [
                _Header(
                  home: data.home,
                  onSearchTap: _showSearchSheet,
                  selectedCuisine: _cuisineFilter,
                  onCuisineSelected: (cuisine) => setState(() {
                    _cuisineFilter = _cuisineFilter == cuisine ? null : cuisine;
                  }),
                ),
                const SizedBox(height: 18),
                _RoleSelector(
                  selected: _role,
                  onSelected: (role) {
                    if (_role == role) return;
                    setState(() {
                      _role = role;
                      // Reload so the role-gated fetches in `_load`
                      // actually run for the newly selected workspace.
                      _snapshotFuture = _load();
                    });
                  },
                ),
                const SizedBox(height: 18),
                _buildRolePanel(data),
              ],
            ),
          );
        },
      ),
    );
  }

  Widget _buildRolePanel(_FigoSnapshot data) {
    final cuisine = _cuisineFilter;
    final home = cuisine == null
        ? data.home
        : _FigoHome(
            cuisines: data.home.cuisines,
            restaurants: data.home.restaurants
                .where((r) => r.cuisines
                    .any((c) => c.toLowerCase() == cuisine.toLowerCase()))
                .toList(),
          );
    return switch (_role) {
      _FigoRole.customer => _CustomerPanel(
        home: home,
        cart: data.cart,
        orders: data.orders,
        addresses: data.addresses,
        walletBalance: data.walletBalance,
        paymentMethod: _paymentMethod,
        tracking: data.tracking,
        batch: data.batch,
        onPaymentMethodChanged: (method) =>
            setState(() => _paymentMethod = method),
        onAddQuickItem: (restaurant) =>
            _runAction(() => _addFirstRecommendedItem(restaurant)),
        onPlaceOrder: (address, method) =>
            _runAction(() => _placeOrder(address, method)),
        onRateOrder: (order, target) =>
            _runAction(() => _rateOrder(order, target)),
        onCancelOrder: (order) => _runAction(() => _cancelOrder(order)),
        onAddAddress: _showAddAddressSheet,
      ),
      _FigoRole.restaurant => _RestaurantPanel(
        restaurants: data.partnerRestaurants,
        onOrderAction: (order, action) =>
            _runAction(() => _partnerOrderAction(order, action)),
      ),
      _FigoRole.delivery => _DeliveryPanel(
        workspace: data.delivery,
        onAssignmentAction: (assignment, action) =>
            _runAction(() => _deliveryAssignmentAction(assignment, action)),
        onUpdateLocation: () => _runAction(_updateDeliveryLocation),
      ),
      _FigoRole.admin => _AdminPanel(
        workspace: data.admin,
        onOrderAction: (order, action) =>
            _runAction(() => _adminOrderAction(order, action)),
        onGenerateSettlements: () => _runAction(_generateSettlements),
      ),
    };
  }
}

class _Header extends StatelessWidget {
  const _Header({
    required this.home,
    required this.onSearchTap,
    required this.selectedCuisine,
    required this.onCuisineSelected,
  });

  final _FigoHome home;
  final VoidCallback onSearchTap;
  final String? selectedCuisine;
  final ValueChanged<String> onCuisineSelected;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text('Food in GO', style: AppTextStyles.h1),
        const SizedBox(height: 6),
        Text(
          'Restaurants, home kitchens, delivery partners, and operations in one FiGo workspace.',
          style: AppTextStyles.body.copyWith(color: AppColors.textTertiary),
        ),
        const SizedBox(height: 16),
        _SearchBox(onTap: onSearchTap),
        const SizedBox(height: 14),
        _CuisineChips(
          cuisines: home.cuisines,
          selected: selectedCuisine,
          onSelected: onCuisineSelected,
        ),
      ],
    );
  }
}

class _RoleSelector extends StatelessWidget {
  const _RoleSelector({required this.selected, required this.onSelected});

  final _FigoRole selected;
  final ValueChanged<_FigoRole> onSelected;

  @override
  Widget build(BuildContext context) {
    const entries = [
      (_FigoRole.customer, Icons.shopping_bag_rounded, 'Customer'),
      (_FigoRole.restaurant, Icons.storefront_rounded, 'Partner'),
      (_FigoRole.delivery, Icons.delivery_dining_rounded, 'Delivery'),
      (_FigoRole.admin, Icons.admin_panel_settings_rounded, 'Admin'),
    ];
    return SizedBox(
      height: 42,
      child: ListView.separated(
        scrollDirection: Axis.horizontal,
        itemCount: entries.length,
        separatorBuilder: (_, _) => const SizedBox(width: 8),
        itemBuilder: (context, index) {
          final entry = entries[index];
          final isSelected = selected == entry.$1;
          return ChoiceChip(
            selected: isSelected,
            onSelected: (_) => onSelected(entry.$1),
            avatar: Icon(
              entry.$2,
              size: 18,
              color: isSelected ? Colors.black : AppColors.textMuted,
            ),
            label: Text(entry.$3),
            labelStyle: AppTextStyles.label.copyWith(
              color: isSelected ? Colors.black : AppColors.textSecondary,
            ),
            selectedColor: const Color(0xFFFFB05F),
            backgroundColor: AppColors.bgSecondary,
            side: const BorderSide(color: AppColors.borderSubtle),
          );
        },
      ),
    );
  }
}

class _CustomerPanel extends StatelessWidget {
  const _CustomerPanel({
    required this.home,
    required this.cart,
    required this.orders,
    required this.addresses,
    required this.walletBalance,
    required this.paymentMethod,
    required this.tracking,
    required this.batch,
    required this.onPaymentMethodChanged,
    required this.onAddQuickItem,
    required this.onPlaceOrder,
    required this.onRateOrder,
    required this.onCancelOrder,
    required this.onAddAddress,
  });

  final _FigoHome home;
  final _FigoCart? cart;
  final List<_FigoOrder> orders;
  final List<_FigoAddress> addresses;
  final double walletBalance;
  final String paymentMethod;
  final _OrderTracking? tracking;
  final _OrderBatch? batch;
  final ValueChanged<String> onPaymentMethodChanged;
  final ValueChanged<_Restaurant> onAddQuickItem;
  final void Function(_FigoAddress address, String paymentMethod) onPlaceOrder;
  final void Function(_FigoOrder order, String target) onRateOrder;
  final void Function(_FigoOrder order) onCancelOrder;
  final VoidCallback onAddAddress;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        _OfferCard(),
        const SizedBox(height: 18),
        _SectionHeader(
          title: 'Nearby restaurants',
          subtitle: '${home.restaurants.length} live',
        ),
        const SizedBox(height: 12),
        if (home.restaurants.isEmpty)
          const _EmptyState(text: 'No restaurants are live in your area yet.')
        else
          for (final restaurant in home.restaurants.take(8)) ...[
            _RestaurantCard(
              restaurant: restaurant,
              trailing: FilledButton.icon(
                onPressed: () => onAddQuickItem(restaurant),
                icon: const Icon(Icons.add_shopping_cart_rounded, size: 17),
                label: const Text('Add'),
              ),
            ),
            const SizedBox(height: 12),
          ],
        const SizedBox(height: 8),
        _CartCard(
          cart: cart,
          addresses: addresses,
          walletBalance: walletBalance,
          paymentMethod: paymentMethod,
          onPaymentMethodChanged: onPaymentMethodChanged,
          onPlaceOrder: onPlaceOrder,
          onAddAddress: onAddAddress,
        ),
        if (tracking != null) ...[
          const SizedBox(height: 18),
          if (batch != null && batch!.total > 1) ...[
            _BatchBanner(batch: batch!),
            const SizedBox(height: 12),
          ],
          _TrackingCard(tracking: tracking!),
        ],
        const SizedBox(height: 18),
        _OrdersList(
          title: 'My orders',
          orders: orders,
          actions: (order) {
            if (order.status == 'DELIVERED') {
              return [
                OutlinedButton.icon(
                  onPressed: () => onRateOrder(order, 'restaurant'),
                  icon: const Icon(Icons.storefront_rounded, size: 16),
                  label: const Text('Rate restaurant'),
                ),
                OutlinedButton.icon(
                  onPressed: () => onRateOrder(order, 'delivery'),
                  icon: const Icon(Icons.delivery_dining_rounded, size: 16),
                  label: const Text('Rate rider'),
                ),
              ];
            }
            // Pre-kitchen statuses are customer-cancellable; the server
            // is the final arbiter (409 once the restaurant is cooking).
            if (const {'PLACED', 'PENDING_PAYMENT', 'CONFIRMED'}
                .contains(order.status)) {
              return [
                OutlinedButton.icon(
                  onPressed: () => onCancelOrder(order),
                  icon: const Icon(Icons.cancel_outlined, size: 16),
                  label: const Text('Cancel order'),
                ),
              ];
            }
            return const [];
          },
        ),
      ],
    );
  }
}

class _RestaurantPanel extends StatelessWidget {
  const _RestaurantPanel({
    required this.restaurants,
    required this.onOrderAction,
  });

  final List<_PartnerRestaurant> restaurants;
  final void Function(_FigoOrder order, String action) onOrderAction;

  @override
  Widget build(BuildContext context) {
    if (restaurants.isEmpty) {
      return const _EmptyState(
        text: 'No partner restaurants found for this account.',
      );
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        _SectionHeader(
          title: 'Restaurant partner',
          subtitle: '${restaurants.length} restaurant profiles',
        ),
        const SizedBox(height: 12),
        for (final restaurant in restaurants) ...[
          _InfoTile(
            icon: Icons.storefront_rounded,
            title: restaurant.name,
            subtitle: '${restaurant.city} - ${restaurant.status}',
            trailing: restaurant.isAcceptingOrders ? 'Accepting' : 'Paused',
          ),
          const SizedBox(height: 10),
        ],
        const SizedBox(height: 8),
        Text(
          'Menu, documents, images, and order status APIs are available from the backend. The web console now exposes the richer management surface.',
          style: AppTextStyles.bodySmall,
        ),
      ],
    );
  }
}

class _DeliveryPanel extends StatelessWidget {
  const _DeliveryPanel({
    required this.workspace,
    required this.onAssignmentAction,
    required this.onUpdateLocation,
  });

  final _DeliveryWorkspace workspace;
  final void Function(_DeliveryAssignment assignment, String action)
  onAssignmentAction;
  final VoidCallback onUpdateLocation;

  @override
  Widget build(BuildContext context) {
    final assignment = workspace.currentAssignment;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        _SectionHeader(
          title: 'Delivery partner',
          subtitle: workspace.profile?.status ?? 'Profile not found',
        ),
        const SizedBox(height: 12),
        _MetricGrid(
          metrics: [
            _Metric('Today', _money(workspace.earnings?.today ?? 0)),
            _Metric('Total', _money(workspace.earnings?.total ?? 0)),
          ],
        ),
        const SizedBox(height: 14),
        SizedBox(
          width: double.infinity,
          child: OutlinedButton.icon(
            onPressed: onUpdateLocation,
            icon: const Icon(Icons.my_location_rounded),
            label: const Text('Update rider location'),
          ),
        ),
        const SizedBox(height: 14),
        if (assignment == null)
          const _EmptyState(text: 'No active assignment right now.')
        else
          _AssignmentCard(
            assignment: assignment,
            tracking: workspace.tracking,
            onAction: onAssignmentAction,
          ),
      ],
    );
  }
}

class _AdminPanel extends StatelessWidget {
  const _AdminPanel({
    required this.workspace,
    required this.onOrderAction,
    required this.onGenerateSettlements,
  });

  final _AdminWorkspace workspace;
  final void Function(_FigoOrder order, String action) onOrderAction;
  final VoidCallback onGenerateSettlements;

  @override
  Widget build(BuildContext context) {
    final dashboard = workspace.dashboard;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        _SectionHeader(
          title: 'Admin operations',
          subtitle: 'Food service control plane',
        ),
        const SizedBox(height: 12),
        _MetricGrid(
          metrics: [
            _Metric('Orders', '${dashboard?.totalOrdersToday ?? 0}'),
            _Metric('GMV', _money(dashboard?.gmvToday ?? 0)),
            _Metric('Restaurants', '${dashboard?.activeRestaurants ?? 0}'),
            _Metric('Riders', '${dashboard?.onlineDeliveryPartners ?? 0}'),
            _Metric('Restaurant payouts', '${workspace.restaurantSettlements}'),
            _Metric('Rider payouts', '${workspace.deliverySettlements}'),
            _Metric('Audits', '${workspace.auditLogs}'),
          ],
        ),
        const SizedBox(height: 14),
        SizedBox(
          width: double.infinity,
          child: FilledButton.icon(
            onPressed: onGenerateSettlements,
            icon: const Icon(Icons.account_balance_wallet_rounded, size: 18),
            label: const Text('Generate settlements'),
          ),
        ),
        const SizedBox(height: 18),
        _OrdersList(
          title: 'Live orders',
          orders: workspace.orders,
          actions: (order) => [
            OutlinedButton(
              onPressed: () => onOrderAction(order, 'cancel'),
              child: const Text('Cancel'),
            ),
            OutlinedButton(
              onPressed: () => onOrderAction(order, 'refund'),
              child: const Text('Refund'),
            ),
          ],
        ),
      ],
    );
  }
}

class _AssignmentCard extends StatelessWidget {
  const _AssignmentCard({
    required this.assignment,
    required this.tracking,
    required this.onAction,
  });

  final _DeliveryAssignment assignment;
  final _AssignmentTracking? tracking;
  final void Function(_DeliveryAssignment assignment, String action) onAction;

  @override
  Widget build(BuildContext context) {
    return _Panel(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              const Icon(
                Icons.delivery_dining_rounded,
                color: Color(0xFF10B981),
              ),
              const SizedBox(width: 10),
              Expanded(
                child: Text(
                  assignment.restaurantName,
                  style: AppTextStyles.h3,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              _StatusPill(status: assignment.status),
            ],
          ),
          const SizedBox(height: 10),
          Text(
            '${assignment.orderNumber} - ${_money(assignment.payout)} payout',
            style: AppTextStyles.bodySmall,
          ),
          if (tracking?.lastLatitude != null &&
              tracking?.lastLongitude != null) ...[
            const SizedBox(height: 8),
            _CoordinateLine(
              label: 'Latest GPS',
              latitude: tracking!.lastLatitude,
              longitude: tracking!.lastLongitude,
            ),
          ],
          const SizedBox(height: 14),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              _ActionButton('Accept', () => onAction(assignment, 'accept')),
              _ActionButton(
                'Arrived',
                () => onAction(assignment, 'arrived-restaurant'),
              ),
              _ActionButton(
                'Picked up',
                () => onAction(assignment, 'picked-up'),
              ),
              _ActionButton(
                'Delivered',
                () => onAction(assignment, 'delivered'),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

class _CartCard extends StatelessWidget {
  const _CartCard({
    required this.cart,
    required this.addresses,
    required this.walletBalance,
    required this.paymentMethod,
    required this.onPaymentMethodChanged,
    required this.onPlaceOrder,
    required this.onAddAddress,
  });

  final _FigoCart? cart;
  final List<_FigoAddress> addresses;
  final double walletBalance;
  final String paymentMethod;
  final ValueChanged<String> onPaymentMethodChanged;
  final void Function(_FigoAddress address, String paymentMethod) onPlaceOrder;
  final VoidCallback onAddAddress;

  @override
  Widget build(BuildContext context) {
    final items = cart?.items ?? const <_CartItem>[];
    final defaultAddress = _firstOrNull(
      addresses.where((address) => address.isDefault),
    );
    final checkoutAddress = defaultAddress ?? _firstOrNull(addresses);
    return _Panel(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          _SectionHeader(
            title: 'Cart',
            subtitle: items.isEmpty ? 'Empty' : '${items.length} items',
          ),
          const SizedBox(height: 10),
          if (items.isEmpty)
            Text(
              'Add an item from a nearby restaurant.',
              style: AppTextStyles.bodySmall,
            )
          else
            for (final item in items) ...[
              Row(
                children: [
                  Expanded(child: Text(item.name, style: AppTextStyles.body)),
                  Text('x${item.quantity}', style: AppTextStyles.labelSmall),
                  const SizedBox(width: 8),
                  Text(_money(item.lineTotal), style: AppTextStyles.label),
                ],
              ),
              const SizedBox(height: 8),
            ],
          if (cart != null) ...[
            const Divider(color: AppColors.borderSubtle),
            Row(
              children: [
                Expanded(child: Text('Payable', style: AppTextStyles.h3)),
                Text(_money(cart!.finalAmount), style: AppTextStyles.h3),
              ],
            ),
            const SizedBox(height: 12),
            Wrap(
              spacing: 8,
              children: [
                for (final method in const ['COD', 'WALLET', 'ONLINE'])
                  ChoiceChip(
                    selected: paymentMethod == method,
                    label: Text(method),
                    onSelected: (_) => onPaymentMethodChanged(method),
                  ),
              ],
            ),
            const SizedBox(height: 10),
            Text(switch (paymentMethod) {
              'COD' => 'Cash payment confirms the order immediately.',
              // Phase 2 §D4: wallet is "coming soon" until wallet-service ships.
              'WALLET' =>
                'VChat wallet is launching in this Phase 2 sprint. Use COD or Online for now.',
              _ =>
                'Online creates a payment intent. Complete prepaid checkout on web.',
            }, style: AppTextStyles.bodySmall),
            const SizedBox(height: 12),
            if (checkoutAddress == null)
              SizedBox(
                width: double.infinity,
                child: OutlinedButton.icon(
                  onPressed: onAddAddress,
                  icon: const Icon(Icons.add_location_alt_rounded, size: 18),
                  label: const Text('Add delivery address'),
                ),
              )
            else ...[
              Row(
                children: [
                  const Icon(
                    Icons.location_on_rounded,
                    size: 18,
                    color: AppColors.textMuted,
                  ),
                  const SizedBox(width: 6),
                  Expanded(
                    child: Text(
                      checkoutAddress.summary,
                      style: AppTextStyles.bodySmall,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                  TextButton(
                    onPressed: onAddAddress,
                    style: TextButton.styleFrom(
                      padding: const EdgeInsets.symmetric(horizontal: 6),
                      tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                    ),
                    child: const Text('New'),
                  ),
                ],
              ),
              const SizedBox(height: 12),
              SizedBox(
                width: double.infinity,
                child: FilledButton.icon(
                  onPressed: items.isEmpty
                      ? null
                      : () => onPlaceOrder(checkoutAddress, paymentMethod),
                  icon: const Icon(Icons.receipt_long_rounded, size: 18),
                  label: Text('Place $paymentMethod order'),
                ),
              ),
            ],
          ],
        ],
      ),
    );
  }
}

// _BatchBanner explains to the customer that their order is part of a
// multi-pickup batch (P2 — same restaurant, same rider, sequential
// drops). Renders nothing for solo orders — guard at the call site.
class _BatchBanner extends StatelessWidget {
  const _BatchBanner({required this.batch});

  final _OrderBatch batch;

  @override
  Widget build(BuildContext context) {
    final siblings = batch.total - 1;
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: const Color(0xFFFFF7ED),
        border: Border.all(color: const Color(0xFFFCD34D)),
        borderRadius: BorderRadius.circular(10),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            'Your order is being delivered alongside $siblings other order'
            '${siblings == 1 ? '' : 's'} nearby',
            style: AppTextStyles.bodySmall.copyWith(
              fontWeight: FontWeight.w800,
              color: const Color(0xFF92400E),
            ),
          ),
          const SizedBox(height: 4),
          Text(
            'Your stop: ${batch.stop} of ${batch.total}. The rider is '
            'making the trip in a single bundle from the same restaurant '
            '— your ETA reflects the sequence.',
            style: AppTextStyles.labelTiny.copyWith(
              color: const Color(0xFF92400E),
            ),
          ),
        ],
      ),
    );
  }
}

class _TrackingCard extends StatelessWidget {
  const _TrackingCard({required this.tracking});

  final _OrderTracking tracking;

  @override
  Widget build(BuildContext context) {
    return _Panel(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          _SectionHeader(
            title: 'Tracking',
            subtitle: tracking.etaMinutes > 0
                ? '${tracking.orderNumber} - ${tracking.etaMinutes} min ETA'
                : tracking.orderNumber,
          ),
          const SizedBox(height: 10),
          for (final event in tracking.timeline.take(5)) ...[
            Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                const Icon(
                  Icons.check_circle_rounded,
                  color: Color(0xFF10B981),
                  size: 18,
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(event.label, style: AppTextStyles.bodySmall),
                ),
              ],
            ),
            const SizedBox(height: 8),
          ],
          const SizedBox(height: 4),
          _CoordinateLine(
            label: 'Restaurant',
            latitude: tracking.restaurantLatitude,
            longitude: tracking.restaurantLongitude,
          ),
          _CoordinateLine(
            label: 'Rider',
            latitude: tracking.deliveryLatitude,
            longitude: tracking.deliveryLongitude,
          ),
          _CoordinateLine(
            label: 'Customer',
            latitude: tracking.customerLatitude,
            longitude: tracking.customerLongitude,
          ),
        ],
      ),
    );
  }
}

class _CoordinateLine extends StatelessWidget {
  const _CoordinateLine({
    required this.label,
    required this.latitude,
    required this.longitude,
  });

  final String label;
  final double? latitude;
  final double? longitude;

  @override
  Widget build(BuildContext context) {
    final value = latitude == null || longitude == null
        ? 'Pending'
        : '${latitude!.toStringAsFixed(5)}, ${longitude!.toStringAsFixed(5)}';
    return Padding(
      padding: const EdgeInsets.only(top: 6),
      child: Row(
        children: [
          const Icon(
            Icons.location_on_rounded,
            size: 16,
            color: AppColors.textMuted,
          ),
          const SizedBox(width: 6),
          Expanded(child: Text(label, style: AppTextStyles.labelSmall)),
          Text(value, style: AppTextStyles.labelSmall),
        ],
      ),
    );
  }
}

class _OrdersList extends StatelessWidget {
  const _OrdersList({required this.title, required this.orders, this.actions});

  final String title;
  final List<_FigoOrder> orders;
  final List<Widget> Function(_FigoOrder order)? actions;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        _SectionHeader(title: title, subtitle: '${orders.length} records'),
        const SizedBox(height: 12),
        if (orders.isEmpty)
          const _EmptyState(text: 'No orders found.')
        else
          for (final order in orders.take(6)) ...[
            _Panel(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      Expanded(
                        child: Text(
                          order.restaurantName,
                          style: AppTextStyles.h3,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                        ),
                      ),
                      _StatusPill(status: order.status),
                    ],
                  ),
                  const SizedBox(height: 8),
                  Text(
                    '${order.orderNumber} - ${_money(order.finalAmount)}',
                    style: AppTextStyles.bodySmall,
                  ),
                  if (actions != null) ...[
                    const SizedBox(height: 12),
                    Wrap(spacing: 8, children: actions!(order)),
                  ],
                ],
              ),
            ),
            const SizedBox(height: 10),
          ],
      ],
    );
  }
}

class _MetricGrid extends StatelessWidget {
  const _MetricGrid({required this.metrics});

  final List<_Metric> metrics;

  @override
  Widget build(BuildContext context) {
    return GridView.builder(
      shrinkWrap: true,
      physics: const NeverScrollableScrollPhysics(),
      itemCount: metrics.length,
      gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(
        crossAxisCount: 2,
        crossAxisSpacing: 10,
        mainAxisSpacing: 10,
        childAspectRatio: 2.6,
      ),
      itemBuilder: (context, index) {
        final metric = metrics[index];
        return _Panel(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              Text(metric.label, style: AppTextStyles.labelSmall),
              const SizedBox(height: 4),
              Text(metric.value, style: AppTextStyles.h2),
            ],
          ),
        );
      },
    );
  }
}

class _Metric {
  const _Metric(this.label, this.value);

  final String label;
  final String value;
}

class _RestaurantCard extends StatelessWidget {
  const _RestaurantCard({required this.restaurant, this.trailing});

  final _Restaurant restaurant;
  final Widget? trailing;

  @override
  Widget build(BuildContext context) {
    return _Panel(
      padding: EdgeInsets.zero,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          ClipRRect(
            borderRadius: const BorderRadius.vertical(top: Radius.circular(16)),
            child: AspectRatio(
              aspectRatio: 16 / 8,
              child: restaurant.heroImageUrl.isEmpty
                  ? Container(color: const Color(0xFF362315))
                  : AppNetworkImage(restaurant.heroImageUrl, fit: BoxFit.cover, error: Container(color: const Color(0xFF362315))),
            ),
          ),
          Padding(
            padding: const EdgeInsets.all(14),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Expanded(
                      child: Text(
                        restaurant.name,
                        style: AppTextStyles.h3,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                    const Icon(
                      Icons.star_rounded,
                      size: 18,
                      color: Color(0xFFF59E0B),
                    ),
                    Text(
                      restaurant.avgRating.toStringAsFixed(1),
                      style: AppTextStyles.labelSmall,
                    ),
                  ],
                ),
                const SizedBox(height: 6),
                Text(
                  restaurant.cuisines.join(' - '),
                  style: AppTextStyles.bodySmall,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
                const SizedBox(height: 10),
                Row(
                  children: [
                    _MetaChip(
                      icon: Icons.schedule_rounded,
                      label: restaurant.eta,
                    ),
                    const SizedBox(width: 8),
                    _MetaChip(
                      icon: Icons.delivery_dining_rounded,
                      label: _money(restaurant.deliveryFee),
                    ),
                    const Spacer(),
                    ?trailing,
                  ],
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _InfoTile extends StatelessWidget {
  const _InfoTile({
    required this.icon,
    required this.title,
    required this.subtitle,
    this.trailing,
  });

  final IconData icon;
  final String title;
  final String subtitle;
  final String? trailing;

  @override
  Widget build(BuildContext context) {
    return _Panel(
      child: Row(
        children: [
          Icon(icon, color: const Color(0xFFFFB05F)),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(title, style: AppTextStyles.h3),
                const SizedBox(height: 3),
                Text(subtitle, style: AppTextStyles.bodySmall),
              ],
            ),
          ),
          if (trailing != null) _StatusPill(status: trailing!),
        ],
      ),
    );
  }
}

class _Panel extends StatelessWidget {
  const _Panel({required this.child, this.padding});

  final Widget child;
  final EdgeInsetsGeometry? padding;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: padding ?? const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(16),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: child,
    );
  }
}

class _SectionHeader extends StatelessWidget {
  const _SectionHeader({required this.title, this.subtitle});

  final String title;
  final String? subtitle;

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        Expanded(child: Text(title, style: AppTextStyles.h2)),
        if (subtitle != null) Text(subtitle!, style: AppTextStyles.labelSmall),
      ],
    );
  }
}

class _ActionButton extends StatelessWidget {
  const _ActionButton(this.label, this.onPressed);

  final String label;
  final VoidCallback onPressed;

  @override
  Widget build(BuildContext context) {
    return OutlinedButton(onPressed: onPressed, child: Text(label));
  }
}

/// Minimal address form matching food-service CreateAddress:
/// address_line1 + city are required server-side; the rest optional.
/// Pops with the request body, or null on dismiss.
class _AddAddressSheet extends StatefulWidget {
  const _AddAddressSheet();

  @override
  State<_AddAddressSheet> createState() => _AddAddressSheetState();
}

class _AddAddressSheetState extends State<_AddAddressSheet> {
  final _label = TextEditingController(text: 'Home');
  final _receiver = TextEditingController();
  final _phone = TextEditingController();
  final _line1 = TextEditingController();
  final _line2 = TextEditingController();
  final _city = TextEditingController();
  final _postalCode = TextEditingController();
  bool _isDefault = true;

  @override
  void dispose() {
    for (final c in [
      _label,
      _receiver,
      _phone,
      _line1,
      _line2,
      _city,
      _postalCode,
    ]) {
      c.dispose();
    }
    super.dispose();
  }

  void _submit() {
    if (_line1.text.trim().isEmpty || _city.text.trim().isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Address line and city are required.')),
      );
      return;
    }
    Navigator.of(context).pop(<String, dynamic>{
      'label': _label.text.trim(),
      if (_receiver.text.trim().isNotEmpty)
        'receiver_name': _receiver.text.trim(),
      if (_phone.text.trim().isNotEmpty) 'phone': _phone.text.trim(),
      'address_line1': _line1.text.trim(),
      if (_line2.text.trim().isNotEmpty) 'address_line2': _line2.text.trim(),
      'city': _city.text.trim(),
      if (_postalCode.text.trim().isNotEmpty)
        'postal_code': _postalCode.text.trim(),
      'country': 'IN',
      'is_default': _isDefault,
    });
  }

  Widget _field(
    TextEditingController controller,
    String hint, {
    TextInputType? keyboardType,
  }) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 10),
      child: TextField(
        controller: controller,
        keyboardType: keyboardType,
        style: AppTextStyles.body,
        decoration: InputDecoration(
          hintText: hint,
          hintStyle: AppTextStyles.body.copyWith(color: AppColors.textMuted),
          filled: true,
          fillColor: AppColors.bgCard,
          contentPadding:
              const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
          border: OutlineInputBorder(
            borderRadius: BorderRadius.circular(12),
            borderSide: const BorderSide(color: AppColors.borderSubtle),
          ),
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: EdgeInsets.only(
        left: 20,
        right: 20,
        top: 20,
        bottom: 20 + MediaQuery.of(context).viewInsets.bottom,
      ),
      child: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Text('New delivery address', style: AppTextStyles.h3),
            const SizedBox(height: 14),
            _field(_label, 'Label (Home, Work…)'),
            _field(_receiver, 'Receiver name'),
            _field(_phone, 'Phone', keyboardType: TextInputType.phone),
            _field(_line1, 'House / street *'),
            _field(_line2, 'Area / landmark'),
            _field(_city, 'City *'),
            _field(_postalCode, 'PIN code', keyboardType: TextInputType.number),
            SwitchListTile(
              value: _isDefault,
              onChanged: (v) => setState(() => _isDefault = v),
              title: Text('Set as default', style: AppTextStyles.body),
              contentPadding: EdgeInsets.zero,
            ),
            const SizedBox(height: 6),
            FilledButton(
              onPressed: _submit,
              child: const Text('Save address'),
            ),
          ],
        ),
      ),
    );
  }
}

class _SearchBox extends StatelessWidget {
  const _SearchBox({required this.onTap});

  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(14),
      child: Container(
        height: 48,
        padding: const EdgeInsets.symmetric(horizontal: 14),
        decoration: BoxDecoration(
          color: AppColors.bgSecondary,
          borderRadius: BorderRadius.circular(14),
          border: Border.all(color: AppColors.borderSubtle),
        ),
        child: Row(
          children: [
            const Icon(Icons.search_rounded, color: AppColors.textMuted),
            const SizedBox(width: 10),
            Text(
              'Search biryani, dosa, meals',
              style: AppTextStyles.body.copyWith(color: AppColors.textMuted),
            ),
          ],
        ),
      ),
    );
  }
}

/// Live search over GET /v1/food/search — one query returns matching
/// restaurants AND dishes ({data: {restaurants, dishes}}).
class _FigoSearchSheet extends ConsumerStatefulWidget {
  const _FigoSearchSheet();

  @override
  ConsumerState<_FigoSearchSheet> createState() => _FigoSearchSheetState();
}

class _FigoSearchSheetState extends ConsumerState<_FigoSearchSheet> {
  final _controller = TextEditingController();
  Timer? _debounce;
  bool _loading = false;
  List<_Restaurant> _restaurants = const [];
  List<Map<String, dynamic>> _dishes = const [];

  @override
  void dispose() {
    _debounce?.cancel();
    _controller.dispose();
    super.dispose();
  }

  void _onChanged(String value) {
    _debounce?.cancel();
    _debounce = Timer(const Duration(milliseconds: 350), () => _run(value));
  }

  Future<void> _run(String query) async {
    final q = query.trim();
    if (q.isEmpty) {
      setState(() {
        _restaurants = const [];
        _dishes = const [];
      });
      return;
    }
    setState(() => _loading = true);
    try {
      final res = await ref.read(apiClientProvider).get(
        '/v1/food/search',
        queryParameters: {'q': q, 'limit': 20},
      );
      final data = _responseData(res.data);
      if (!mounted) return;
      setState(() {
        _restaurants =
            _itemsFromKey(data, 'restaurants').map(_Restaurant.fromJson).toList();
        _dishes = _itemsFromKey(data, 'dishes');
        _loading = false;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding:
          EdgeInsets.only(bottom: MediaQuery.of(context).viewInsets.bottom),
      child: SizedBox(
        height: MediaQuery.of(context).size.height * 0.75,
        child: Column(
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
              child: TextField(
                controller: _controller,
                autofocus: true,
                onChanged: _onChanged,
                onSubmitted: _run,
                style: AppTextStyles.body,
                decoration: InputDecoration(
                  hintText: 'Search biryani, dosa, meals',
                  hintStyle:
                      AppTextStyles.body.copyWith(color: AppColors.textMuted),
                  prefixIcon:
                      const Icon(Icons.search_rounded, color: AppColors.textMuted),
                  filled: true,
                  fillColor: AppColors.bgCard,
                  border: OutlineInputBorder(
                    borderRadius: BorderRadius.circular(14),
                    borderSide: const BorderSide(color: AppColors.borderSubtle),
                  ),
                ),
              ),
            ),
            if (_loading) const LinearProgressIndicator(minHeight: 2),
            Expanded(
              child: ListView(
                padding: const EdgeInsets.fromLTRB(16, 8, 16, 24),
                children: [
                  if (_restaurants.isEmpty &&
                      _dishes.isEmpty &&
                      !_loading &&
                      _controller.text.trim().isNotEmpty)
                    Padding(
                      padding: const EdgeInsets.only(top: 32),
                      child: Center(
                        child: Text(
                          'No matches. Try another dish or restaurant.',
                          style: AppTextStyles.bodySmall
                              .copyWith(color: AppColors.textDim),
                        ),
                      ),
                    ),
                  if (_restaurants.isNotEmpty) ...[
                    Text('Restaurants', style: AppTextStyles.h3),
                    const SizedBox(height: 8),
                    for (final r in _restaurants) ...[
                      ListTile(
                        contentPadding: EdgeInsets.zero,
                        leading: const Icon(Icons.storefront_rounded,
                            color: AppColors.textMuted),
                        title: Text(r.name, style: AppTextStyles.body),
                        subtitle: Text(
                          r.cuisines.join(' - '),
                          style: AppTextStyles.bodySmall
                              .copyWith(color: AppColors.textTertiary),
                        ),
                        trailing: Text('★ ${r.avgRating.toStringAsFixed(1)}',
                            style: AppTextStyles.labelSmall),
                      ),
                    ],
                    const SizedBox(height: 12),
                  ],
                  if (_dishes.isNotEmpty) ...[
                    Text('Dishes', style: AppTextStyles.h3),
                    const SizedBox(height: 8),
                    for (final dish in _dishes)
                      ListTile(
                        contentPadding: EdgeInsets.zero,
                        leading: const Icon(Icons.restaurant_rounded,
                            color: AppColors.textMuted),
                        title: Text(
                          dish['name']?.toString() ?? 'Dish',
                          style: AppTextStyles.body,
                        ),
                        subtitle: Text(
                          dish['restaurant_name']?.toString() ?? '',
                          style: AppTextStyles.bodySmall
                              .copyWith(color: AppColors.textTertiary),
                        ),
                        trailing: Text(
                          _money(
                            ((dish['discount_price'] ?? dish['base_price'])
                                        as num?)
                                    ?.toDouble() ??
                                0,
                          ),
                          style: AppTextStyles.label,
                        ),
                      ),
                  ],
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _CuisineChips extends StatelessWidget {
  const _CuisineChips({
    required this.cuisines,
    required this.selected,
    required this.onSelected,
  });

  final List<String> cuisines;
  final String? selected;
  final ValueChanged<String> onSelected;

  @override
  Widget build(BuildContext context) {
    final values = cuisines.isEmpty ? ['Biryani', 'Meals', 'Dosa'] : cuisines;
    return SizedBox(
      height: 38,
      child: ListView.separated(
        scrollDirection: Axis.horizontal,
        itemBuilder: (context, index) {
          final cuisine = values[index];
          final isActive = selected?.toLowerCase() == cuisine.toLowerCase();
          return InkWell(
            onTap: () => onSelected(cuisine),
            borderRadius: BorderRadius.circular(999),
            child: Container(
              padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 9),
              decoration: BoxDecoration(
                color: isActive
                    ? AppColors.postbookPrimary.withValues(alpha: 0.18)
                    : AppColors.bgSecondary,
                borderRadius: BorderRadius.circular(999),
                border: Border.all(
                  color: isActive
                      ? AppColors.postbookPrimary
                      : AppColors.borderSubtle,
                ),
              ),
              child: Text(
                cuisine,
                style: AppTextStyles.labelSmall.copyWith(
                  color: isActive ? AppColors.postbookPrimary : null,
                ),
              ),
            ),
          );
        },
        separatorBuilder: (_, _) => const SizedBox(width: 8),
        itemCount: values.length,
      ),
    );
  }
}

class _OfferCard extends StatelessWidget {
  @override
  Widget build(BuildContext context) {
    return _Panel(
      child: Row(
        children: [
          const Icon(Icons.local_offer_rounded, color: Color(0xFFF97316)),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text('FIGO50', style: AppTextStyles.h3),
                const SizedBox(height: 2),
                Text(
                  'Get Rs 50 off on orders above Rs 199.',
                  style: AppTextStyles.bodySmall,
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _MetaChip extends StatelessWidget {
  const _MetaChip({required this.icon, required this.label});

  final IconData icon;
  final String label;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 5),
      decoration: BoxDecoration(
        color: AppColors.bgPrimary,
        borderRadius: BorderRadius.circular(999),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, size: 14, color: AppColors.textMuted),
          const SizedBox(width: 4),
          Text(label, style: AppTextStyles.labelSmall),
        ],
      ),
    );
  }
}

class _StatusPill extends StatelessWidget {
  const _StatusPill({required this.status});

  final String status;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 5),
      decoration: BoxDecoration(
        color: const Color(0x1AF97316),
        borderRadius: BorderRadius.circular(999),
      ),
      child: Text(status, style: AppTextStyles.labelSmall),
    );
  }
}

class _FigoSkeleton extends StatelessWidget {
  const _FigoSkeleton();

  @override
  Widget build(BuildContext context) {
    return ListView.separated(
      padding: const EdgeInsets.all(18),
      itemBuilder: (_, index) => Container(
        height: index == 0 ? 120 : 190,
        decoration: BoxDecoration(
          color: AppColors.bgSecondary,
          borderRadius: BorderRadius.circular(16),
        ),
      ),
      separatorBuilder: (_, _) => const SizedBox(height: AppSpacing.xxl),
      itemCount: 4,
    );
  }
}

class _FigoError extends StatelessWidget {
  const _FigoError({required this.onRetry});

  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(
              Icons.error_outline_rounded,
              size: 48,
              color: AppColors.textMuted,
            ),
            const SizedBox(height: 12),
            Text('FiGo is unavailable', style: AppTextStyles.h2),
            const SizedBox(height: 8),
            Text(
              'We could not load the food workspace right now.',
              style: AppTextStyles.body.copyWith(color: AppColors.textTertiary),
              textAlign: TextAlign.center,
            ),
            const SizedBox(height: 18),
            FilledButton(onPressed: onRetry, child: const Text('Retry')),
          ],
        ),
      ),
    );
  }
}

class _EmptyState extends StatelessWidget {
  const _EmptyState({required this.text});

  final String text;

  @override
  Widget build(BuildContext context) {
    return _Panel(
      child: Text(text, style: AppTextStyles.body, textAlign: TextAlign.center),
    );
  }
}

class _FigoSnapshot {
  const _FigoSnapshot({
    required this.home,
    required this.cart,
    required this.orders,
    required this.addresses,
    required this.walletBalance,
    required this.tracking,
    required this.batch,
    required this.partnerRestaurants,
    required this.delivery,
    required this.admin,
  });

  final _FigoHome home;
  final _FigoCart? cart;
  final List<_FigoOrder> orders;
  final List<_FigoAddress> addresses;
  final double walletBalance;
  final _OrderTracking? tracking;
  final _OrderBatch? batch;
  final List<_PartnerRestaurant> partnerRestaurants;
  final _DeliveryWorkspace delivery;
  final _AdminWorkspace admin;

  factory _FigoSnapshot.empty() {
    return const _FigoSnapshot(
      home: _FigoHome(cuisines: [], restaurants: []),
      cart: null,
      orders: [],
      addresses: [],
      walletBalance: 0,
      tracking: null,
      batch: null,
      partnerRestaurants: [],
      delivery: _DeliveryWorkspace(),
      admin: _AdminWorkspace(),
    );
  }
}

// _OrderBatch is rendered as the "delivered alongside N other orders"
// banner on the tracked order. members[].sequence is the stop number
// (1-based); the customer's own stop is captured separately so the UI
// can render "Stop X of Y" without re-scanning.
class _OrderBatch {
  const _OrderBatch({
    required this.id,
    required this.stop,
    required this.total,
  });

  final String id;
  final int stop;
  final int total;

  factory _OrderBatch.fromJson(Map<String, dynamic> json, String orderId) {
    final members = (json['members'] as List?) ?? const [];
    int stop = 0;
    for (final raw in members) {
      if (raw is Map && raw['order_id']?.toString() == orderId) {
        stop = (raw['sequence'] as num?)?.toInt() ?? 0;
        break;
      }
    }
    return _OrderBatch(
      id: json['id']?.toString() ?? '',
      stop: stop,
      total: members.length,
    );
  }
}

class _FigoHome {
  const _FigoHome({required this.cuisines, required this.restaurants});

  final List<String> cuisines;
  final List<_Restaurant> restaurants;

  factory _FigoHome.fromJson(Map<String, dynamic> json) {
    return _FigoHome(
      cuisines: _itemsFromKey(json, 'cuisines')
          .map((item) => item['name']?.toString() ?? '')
          .where((name) => name.isNotEmpty)
          .toList(),
      restaurants: _itemsFromKey(
        json,
        'nearby_restaurants',
      ).map(_Restaurant.fromJson).toList(),
    );
  }
}

class _Restaurant {
  const _Restaurant({
    required this.id,
    required this.name,
    required this.heroImageUrl,
    required this.cuisines,
    required this.avgRating,
    required this.eta,
    required this.deliveryFee,
  });

  final String id;
  final String name;
  final String heroImageUrl;
  final List<String> cuisines;
  final double avgRating;
  final String eta;
  final double deliveryFee;

  factory _Restaurant.fromJson(Map<String, dynamic> json) {
    return _Restaurant(
      id: json['id']?.toString() ?? '',
      name: json['name'] as String? ?? 'Restaurant',
      heroImageUrl: json['hero_image_url'] as String? ?? '',
      cuisines: ((json['cuisines'] as List?) ?? [])
          .map((item) => item.toString())
          .toList(),
      avgRating: (json['avg_rating'] as num?)?.toDouble() ?? 0,
      eta: json['estimated_delivery'] as String? ?? '25-35 min',
      deliveryFee: (json['delivery_fee_estimate'] as num?)?.toDouble() ?? 0,
    );
  }
}

class _FigoCart {
  const _FigoCart({required this.items, required this.finalAmount});

  final List<_CartItem> items;
  final double finalAmount;

  factory _FigoCart.fromJson(Map<String, dynamic> json) {
    final totals = Map<String, dynamic>.from((json['totals'] as Map?) ?? {});
    return _FigoCart(
      items: _itemsFromKey(json, 'items').map(_CartItem.fromJson).toList(),
      finalAmount: (totals['final_amount'] as num?)?.toDouble() ?? 0,
    );
  }
}

class _CartItem {
  const _CartItem({
    required this.name,
    required this.quantity,
    required this.lineTotal,
  });

  final String name;
  final int quantity;
  final double lineTotal;

  factory _CartItem.fromJson(Map<String, dynamic> json) {
    return _CartItem(
      name: json['name'] as String? ?? 'Item',
      quantity: (json['quantity'] as num?)?.toInt() ?? 0,
      lineTotal: (json['line_total'] as num?)?.toDouble() ?? 0,
    );
  }
}

class _FigoAddress {
  const _FigoAddress({
    required this.id,
    required this.label,
    required this.addressLine1,
    required this.city,
    required this.isDefault,
  });

  final String id;
  final String label;
  final String addressLine1;
  final String city;
  final bool isDefault;

  String get summary {
    final parts = [
      if (label.isNotEmpty) label,
      if (addressLine1.isNotEmpty) addressLine1,
      if (city.isNotEmpty) city,
    ];
    return parts.join(' - ');
  }

  factory _FigoAddress.fromJson(Map<String, dynamic> json) {
    return _FigoAddress(
      id: json['id']?.toString() ?? '',
      label: json['label'] as String? ?? '',
      addressLine1: json['address_line1'] as String? ?? '',
      city: json['city'] as String? ?? '',
      isDefault: json['is_default'] as bool? ?? false,
    );
  }
}

class _FigoOrder {
  const _FigoOrder({
    required this.id,
    required this.orderNumber,
    required this.restaurantName,
    required this.status,
    required this.finalAmount,
  });

  final String id;
  final String orderNumber;
  final String restaurantName;
  final String status;
  final double finalAmount;

  factory _FigoOrder.fromJson(Map<String, dynamic> json) {
    final totals = Map<String, dynamic>.from((json['totals'] as Map?) ?? {});
    return _FigoOrder(
      id: json['id']?.toString() ?? '',
      orderNumber: json['order_number'] as String? ?? 'Order',
      restaurantName: json['restaurant_name'] as String? ?? 'Restaurant',
      status: json['status'] as String? ?? 'UNKNOWN',
      finalAmount: (totals['final_amount'] as num?)?.toDouble() ?? 0,
    );
  }
}

class _OrderTracking {
  const _OrderTracking({
    required this.orderNumber,
    required this.timeline,
    required this.etaMinutes,
    this.restaurantLatitude,
    this.restaurantLongitude,
    this.deliveryLatitude,
    this.deliveryLongitude,
    this.customerLatitude,
    this.customerLongitude,
  });

  final String orderNumber;
  final List<_TrackingEvent> timeline;
  final int etaMinutes;
  final double? restaurantLatitude;
  final double? restaurantLongitude;
  final double? deliveryLatitude;
  final double? deliveryLongitude;
  final double? customerLatitude;
  final double? customerLongitude;

  factory _OrderTracking.fromJson(Map<String, dynamic> json) {
    final restaurant = Map<String, dynamic>.from(
      (json['restaurant_location'] as Map?) ?? {},
    );
    final delivery = Map<String, dynamic>.from(
      (json['delivery_location'] as Map?) ?? {},
    );
    final customer = Map<String, dynamic>.from(
      (json['customer_location'] as Map?) ?? {},
    );
    return _OrderTracking(
      orderNumber: json['order_number'] as String? ?? 'Order',
      timeline: _itemsFromKey(
        json,
        'timeline',
      ).map(_TrackingEvent.fromJson).toList(),
      etaMinutes: (json['estimated_delivery_minutes'] as num?)?.toInt() ?? 0,
      restaurantLatitude: (restaurant['latitude'] as num?)?.toDouble(),
      restaurantLongitude: (restaurant['longitude'] as num?)?.toDouble(),
      deliveryLatitude: (delivery['latitude'] as num?)?.toDouble(),
      deliveryLongitude: (delivery['longitude'] as num?)?.toDouble(),
      customerLatitude: (customer['latitude'] as num?)?.toDouble(),
      customerLongitude: (customer['longitude'] as num?)?.toDouble(),
    );
  }
}

class _TrackingEvent {
  const _TrackingEvent({required this.label});

  final String label;

  factory _TrackingEvent.fromJson(Map<String, dynamic> json) {
    return _TrackingEvent(
      label:
          json['label'] as String? ??
          json['to_status'] as String? ??
          json['status'] as String? ??
          'Updated',
    );
  }
}

class _PartnerRestaurant {
  const _PartnerRestaurant({
    required this.name,
    required this.city,
    required this.status,
    required this.isAcceptingOrders,
  });

  final String name;
  final String city;
  final String status;
  final bool isAcceptingOrders;

  factory _PartnerRestaurant.fromJson(Map<String, dynamic> json) {
    return _PartnerRestaurant(
      name: json['name'] as String? ?? 'Restaurant',
      city: json['city'] as String? ?? '',
      status: json['status'] as String? ?? 'PENDING',
      isAcceptingOrders: json['is_accepting_orders'] as bool? ?? false,
    );
  }
}

class _DeliveryWorkspace {
  const _DeliveryWorkspace({
    this.profile,
    this.currentAssignment,
    this.earnings,
    this.tracking,
  });

  final _DeliveryProfile? profile;
  final _DeliveryAssignment? currentAssignment;
  final _DeliveryEarnings? earnings;
  final _AssignmentTracking? tracking;
}

class _DeliveryProfile {
  const _DeliveryProfile({required this.status});

  final String status;

  factory _DeliveryProfile.fromJson(Map<String, dynamic> json) {
    return _DeliveryProfile(status: json['status'] as String? ?? 'PENDING');
  }
}

class _DeliveryAssignment {
  const _DeliveryAssignment({
    required this.id,
    required this.orderNumber,
    required this.restaurantName,
    required this.status,
    required this.payout,
  });

  final String id;
  final String orderNumber;
  final String restaurantName;
  final String status;
  final double payout;

  factory _DeliveryAssignment.fromJson(Map<String, dynamic> json) {
    return _DeliveryAssignment(
      id: json['id']?.toString() ?? '',
      orderNumber: json['order_number'] as String? ?? 'Order',
      restaurantName: json['restaurant_name'] as String? ?? 'Restaurant',
      status: json['status'] as String? ?? 'ASSIGNED',
      payout: (json['delivery_partner_payout'] as num?)?.toDouble() ?? 0,
    );
  }
}

class _DeliveryEarnings {
  const _DeliveryEarnings({required this.today, required this.total});

  final double today;
  final double total;

  factory _DeliveryEarnings.fromJson(Map<String, dynamic> json) {
    return _DeliveryEarnings(
      today:
          (json['earnings_today'] as num?)?.toDouble() ??
          (json['today_earnings'] as num?)?.toDouble() ??
          0,
      total: (json['total_earnings'] as num?)?.toDouble() ?? 0,
    );
  }
}

class _AssignmentTracking {
  const _AssignmentTracking({this.lastLatitude, this.lastLongitude});

  final double? lastLatitude;
  final double? lastLongitude;

  factory _AssignmentTracking.fromJson(Map<String, dynamic> json) {
    final location = Map<String, dynamic>.from(
      (json['delivery_location'] as Map?) ?? {},
    );
    return _AssignmentTracking(
      lastLatitude: (location['latitude'] as num?)?.toDouble(),
      lastLongitude: (location['longitude'] as num?)?.toDouble(),
    );
  }
}

class _AdminWorkspace {
  const _AdminWorkspace({
    this.dashboard,
    this.orders = const [],
    this.restaurantSettlements = 0,
    this.deliverySettlements = 0,
    this.auditLogs = 0,
  });

  final _AdminDashboard? dashboard;
  final List<_FigoOrder> orders;
  final int restaurantSettlements;
  final int deliverySettlements;
  final int auditLogs;
}

class _AdminDashboard {
  const _AdminDashboard({
    required this.totalOrdersToday,
    required this.gmvToday,
    required this.activeRestaurants,
    required this.onlineDeliveryPartners,
  });

  final int totalOrdersToday;
  final double gmvToday;
  final int activeRestaurants;
  final int onlineDeliveryPartners;

  factory _AdminDashboard.fromJson(Map<String, dynamic> json) {
    return _AdminDashboard(
      totalOrdersToday: (json['total_orders_today'] as num?)?.toInt() ?? 0,
      gmvToday: (json['gmv_today'] as num?)?.toDouble() ?? 0,
      activeRestaurants: (json['active_restaurants'] as num?)?.toInt() ?? 0,
      onlineDeliveryPartners:
          (json['online_delivery_partners'] as num?)?.toInt() ?? 0,
    );
  }
}

Map<String, dynamic> _responseData(Object? response) {
  final root = Map<String, dynamic>.from((response as Map?) ?? {});
  final data = root['data'];
  if (data is Map) {
    return Map<String, dynamic>.from(data);
  }
  return root;
}

List<Map<String, dynamic>> _items(Map<String, dynamic> data) {
  return _itemsFromKey(data, 'items');
}

List<Map<String, dynamic>> _itemsFromKey(
  Map<String, dynamic> data,
  String key,
) {
  return ((data[key] as List?) ?? [])
      .whereType<Map>()
      .map((item) => Map<String, dynamic>.from(item))
      .toList();
}

T? _firstOrNull<T>(Iterable<T> values) {
  for (final value in values) {
    return value;
  }
  return null;
}

String _money(double value) {
  return 'Rs ${value.toStringAsFixed(0)}';
}
