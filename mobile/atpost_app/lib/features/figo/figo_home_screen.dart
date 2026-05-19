import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/services/api_client.dart';
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

  @override
  void initState() {
    super.initState();
    _snapshotFuture = _load();
  }

  Future<_FigoSnapshot> _load() async {
    final api = ref.read(apiClientProvider);
    final home = await _safeLoad<_FigoHome>(() async {
      final data = _responseData((await api.get('/v1/food/home')).data);
      return _FigoHome.fromJson(data);
    }, fallback: const _FigoHome(cuisines: [], restaurants: []));
    final cart = await _safeLoad<_FigoCart?>(() async {
      final data = _responseData((await api.get('/v1/food/cart')).data);
      return _FigoCart.fromJson(data);
    }, fallback: null);
    final orders = await _safeLoad<List<_FigoOrder>>(() async {
      final data = _responseData((await api.get('/v1/food/orders')).data);
      return _items(data).map(_FigoOrder.fromJson).toList();
    }, fallback: const []);
    final addresses = await _safeLoad<List<_FigoAddress>>(() async {
      final data = _responseData((await api.get('/v1/food/addresses')).data);
      return _items(data).map(_FigoAddress.fromJson).toList();
    }, fallback: const []);
    // Phase 2 §D4: the food UI no longer fetches or displays a wallet
    // balance. The previous code was reading the creator-earnings
    // ledger (/v1/monetization/wallet, now /creator-ledger) and
    // labelling it "Wallet balance" — a real product bug. The AtPost
    // consumer wallet is shipping in wallet-service in the same
    // Phase 2 sprint; until it lands, we just show "Wallet coming soon"
    // in the UI and hardcode the local balance to 0.
    const double walletBalance = 0;
    final tracking = await _safeLoad<_OrderTracking?>(() async {
      if (orders.isEmpty) return null;
      final data = _responseData(
        (await api.get('/v1/food/orders/${orders.first.id}/tracking')).data,
      );
      return _OrderTracking.fromJson(data);
    }, fallback: null);
    final partnerRestaurants = await _safeLoad<List<_PartnerRestaurant>>(
      () async {
        final data = _responseData(
          (await api.get('/v1/food/partner/restaurants')).data,
        );
        return _items(data).map(_PartnerRestaurant.fromJson).toList();
      },
      fallback: const [],
    );
    final delivery = await _safeLoad<_DeliveryWorkspace>(() async {
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
      final trackingData = await _safeLoad<_AssignmentTracking?>(() async {
        final data = _responseData(
          (await api.get(
            '/v1/food/delivery/assignments/${assignment.id}/tracking',
          )).data,
        );
        return _AssignmentTracking.fromJson(data);
      }, fallback: null);
      return _DeliveryWorkspace(
        profile: _DeliveryProfile.fromJson(profileData),
        currentAssignment: assignment,
        earnings: _DeliveryEarnings.fromJson(earningsData),
        tracking: trackingData,
      );
    }, fallback: const _DeliveryWorkspace());
    final admin = await _safeLoad<_AdminWorkspace>(() async {
      final dashboardData = _responseData(
        (await api.get('/v1/food/admin/dashboard')).data,
      );
      final ordersData = _responseData(
        (await api.get('/v1/food/admin/orders')).data,
      );
      final settlementsData = _responseData(
        (await api.get('/v1/food/admin/settlements/delivery-partners')).data,
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
    await ref
        .read(apiClientProvider)
        .post(
          '/v1/food/delivery/assignments/${assignment.id}/$action',
          options: Options(
            headers: {
              'Idempotency-Key':
                  'figo-delivery-${DateTime.now().microsecondsSinceEpoch}',
            },
          ),
        );
  }

  Future<void> _partnerOrderAction(_FigoOrder order, String action) async {
    await ref
        .read(apiClientProvider)
        .post(
          '/v1/food/partner/orders/${order.id}/$action',
          options: Options(
            headers: {
              'Idempotency-Key':
                  'figo-partner-${DateTime.now().microsecondsSinceEpoch}',
            },
          ),
        );
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
    final response = await api.post(
      '/v1/food/orders',
      data: {
        'address_id': address.id,
        'payment_method': paymentMethod,
        'customer_instruction': 'Placed from FiGo mobile',
      },
      options: Options(
        headers: {
          'Idempotency-Key':
              'figo-mobile-${DateTime.now().microsecondsSinceEpoch}',
        },
      ),
    );
    final order = _FigoOrder.fromJson(_responseData(response.data));
    if (paymentMethod != 'COD') {
      final intent = _responseData(
        (await api.post(
          '/v1/food/orders/${order.id}/payments/intents',
          data: {'method': paymentMethod},
          options: Options(
            headers: {
              'Idempotency-Key':
                  'figo-pay-${DateTime.now().microsecondsSinceEpoch}',
            },
          ),
        )).data,
      );
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
    await ref
        .read(apiClientProvider)
        .post(
          '/v1/food/admin/orders/${order.id}/$action',
          data: {'reason': 'Updated from mobile FiGo console'},
          options: action == 'refund'
              ? Options(
                  headers: {
                    'Idempotency-Key':
                        'figo-admin-${DateTime.now().microsecondsSinceEpoch}',
                  },
                )
              : null,
        );
  }

  Future<void> _generateSettlements() async {
    final now = DateTime.now();
    final start = now.subtract(const Duration(days: 7));
    String fmt(DateTime date) =>
        '${date.year.toString().padLeft(4, '0')}-${date.month.toString().padLeft(2, '0')}-${date.day.toString().padLeft(2, '0')}';
    await ref
        .read(apiClientProvider)
        .post(
          '/v1/food/admin/settlements/generate',
          data: {'period_start': fmt(start), 'period_end': fmt(now)},
          options: Options(
            headers: {
              'Idempotency-Key':
                  'figo-settle-${DateTime.now().microsecondsSinceEpoch}',
            },
          ),
        );
  }

  @override
  Widget build(BuildContext context) {
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
                _Header(home: data.home),
                const SizedBox(height: 18),
                _RoleSelector(
                  selected: _role,
                  onSelected: (role) => setState(() => _role = role),
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
    return switch (_role) {
      _FigoRole.customer => _CustomerPanel(
        home: data.home,
        cart: data.cart,
        orders: data.orders,
        addresses: data.addresses,
        walletBalance: data.walletBalance,
        paymentMethod: _paymentMethod,
        tracking: data.tracking,
        onPaymentMethodChanged: (method) =>
            setState(() => _paymentMethod = method),
        onAddQuickItem: (restaurant) =>
            _runAction(() => _addFirstRecommendedItem(restaurant)),
        onPlaceOrder: (address, method) =>
            _runAction(() => _placeOrder(address, method)),
        onRateOrder: (order, target) =>
            _runAction(() => _rateOrder(order, target)),
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
  const _Header({required this.home});

  final _FigoHome home;

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
        _SearchBox(),
        const SizedBox(height: 14),
        _CuisineChips(cuisines: home.cuisines),
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
    required this.onPaymentMethodChanged,
    required this.onAddQuickItem,
    required this.onPlaceOrder,
    required this.onRateOrder,
  });

  final _FigoHome home;
  final _FigoCart? cart;
  final List<_FigoOrder> orders;
  final List<_FigoAddress> addresses;
  final double walletBalance;
  final String paymentMethod;
  final _OrderTracking? tracking;
  final ValueChanged<String> onPaymentMethodChanged;
  final ValueChanged<_Restaurant> onAddQuickItem;
  final void Function(_FigoAddress address, String paymentMethod) onPlaceOrder;
  final void Function(_FigoOrder order, String target) onRateOrder;

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
        ),
        if (tracking != null) ...[
          const SizedBox(height: 18),
          _TrackingCard(tracking: tracking!),
        ],
        const SizedBox(height: 18),
        _OrdersList(
          title: 'My orders',
          orders: orders,
          actions: (order) => order.status == 'DELIVERED'
              ? [
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
                ]
              : const [],
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
  });

  final _FigoCart? cart;
  final List<_FigoAddress> addresses;
  final double walletBalance;
  final String paymentMethod;
  final ValueChanged<String> onPaymentMethodChanged;
  final void Function(_FigoAddress address, String paymentMethod) onPlaceOrder;

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
              Text(
                'Add a delivery address in account settings before checkout.',
                style: AppTextStyles.bodySmall,
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
                  : Image.network(
                      restaurant.heroImageUrl,
                      fit: BoxFit.cover,
                      errorBuilder: (_, _, _) =>
                          Container(color: const Color(0xFF362315)),
                    ),
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

class _SearchBox extends StatelessWidget {
  @override
  Widget build(BuildContext context) {
    return Container(
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
    );
  }
}

class _CuisineChips extends StatelessWidget {
  const _CuisineChips({required this.cuisines});

  final List<String> cuisines;

  @override
  Widget build(BuildContext context) {
    final values = cuisines.isEmpty ? ['Biryani', 'Meals', 'Dosa'] : cuisines;
    return SizedBox(
      height: 38,
      child: ListView.separated(
        scrollDirection: Axis.horizontal,
        itemBuilder: (context, index) => Container(
          padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 9),
          decoration: BoxDecoration(
            color: AppColors.bgSecondary,
            borderRadius: BorderRadius.circular(999),
            border: Border.all(color: AppColors.borderSubtle),
          ),
          child: Text(values[index], style: AppTextStyles.labelSmall),
        ),
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
      partnerRestaurants: [],
      delivery: _DeliveryWorkspace(),
      admin: _AdminWorkspace(),
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
