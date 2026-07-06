import 'package:feature_billpay/billpay/billpay_account_detail_screen.dart';
import 'package:feature_billpay/billpay/billpay_add_account_screen.dart';
import 'package:feature_billpay/billpay/billpay_category_screen.dart';
import 'package:feature_billpay/billpay/billpay_home_screen.dart';
import 'package:feature_billpay/billpay/billpay_payments_screen.dart';
import 'package:feature_billpay/billpay/billpay_receipt_screen.dart';
import 'package:feature_billpay/billpay/billpay_recharge_screen.dart';
import 'package:feature_billpay/billpay/billpay_reminders_screen.dart';
import 'package:feature_billpay/billpay/billpay_scheduled_screen.dart';
import 'package:go_router/go_router.dart';

/// Bill-pay route table (Phase 2 — BBPS via Setu). The app router spreads
/// this into its shell.
List<RouteBase> billpayRoutes() => [
  GoRoute(
    path: '/billpay',
    builder: (_, _) => const BillPayHomeScreen(),
  ),
  GoRoute(
    path: '/billpay/category/:id',
    builder: (context, state) => BillPayCategoryScreen(
      categoryId: state.pathParameters['id']!,
    ),
  ),
  GoRoute(
    path: '/billpay/add-account',
    builder: (context, state) => BillPayAddAccountScreen(
      providerId: state.uri.queryParameters['providerId'] ??
          state.uri.queryParameters['provider'] ??
          '',
    ),
  ),
  GoRoute(
    path: '/billpay/account/:id',
    builder: (context, state) => BillPayAccountDetailScreen(
      accountId: state.pathParameters['id']!,
    ),
  ),
  GoRoute(
    path: '/billpay/recharge',
    builder: (_, _) => const BillPayRechargeScreen(),
  ),
  GoRoute(
    path: '/billpay/payments',
    builder: (_, _) => const BillPayPaymentsScreen(),
  ),
  GoRoute(
    path: '/billpay/payments/:id',
    builder: (context, state) => BillPayReceiptScreen(
      paymentId: state.pathParameters['id']!,
    ),
  ),
  GoRoute(
    path: '/billpay/reminders',
    builder: (_, _) => const BillPayRemindersScreen(),
  ),
  GoRoute(
    path: '/billpay/scheduled',
    builder: (_, _) => const BillPayScheduledScreen(),
  ),
];
