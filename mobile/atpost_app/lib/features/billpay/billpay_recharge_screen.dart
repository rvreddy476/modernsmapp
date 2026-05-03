// Bill-pay mobile recharge — Phase 2.
//
// Three-step flow:
//   1. Phone — input + auto-detect operator/circle. Editable.
//   2. Plan — tabs (Unlimited / Data / Talktime / Topup / Roaming) + search
//      and sort. Tap a plan → step 3.
//   3. Confirm — review summary, payment method picker, "Pay" CTA.
//
// PRIVACY: phone number stays in local state. Telemetry only logs
// `operator` (categorical) and bucketed amount.
//
// IDEMPOTENCY: the pay sheet mints a fresh UUID per call.

import 'dart:async';

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/billpay.dart';
import 'package:atpost_app/data/repositories/billpay_repository.dart';
import 'package:atpost_app/features/billpay/billpay_pay_sheet.dart';
import 'package:atpost_app/providers/billpay_providers.dart';
import 'package:atpost_app/services/money_format.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class BillPayRechargeScreen extends ConsumerStatefulWidget {
  const BillPayRechargeScreen({super.key});

  @override
  ConsumerState<BillPayRechargeScreen> createState() =>
      _BillPayRechargeScreenState();
}

enum _Step { phone, plans, confirm }

class _BillPayRechargeScreenState
    extends ConsumerState<BillPayRechargeScreen> {
  final _phoneController = TextEditingController();
  _Step _step = _Step.phone;
  String? _phone;
  String? _operator;
  String? _circle;
  MobilePlan? _selectedPlan;
  Timer? _debounce;

  @override
  void dispose() {
    _phoneController.dispose();
    _debounce?.cancel();
    super.dispose();
  }

  void _onPhoneChanged(String v) {
    final digits = v.replaceAll(RegExp(r'\D'), '');
    _debounce?.cancel();
    if (digits.length != 10) return;
    _debounce = Timer(const Duration(milliseconds: 400), () async {
      try {
        final oc = await ref
            .read(billpayRepositoryProvider)
            .detectOperatorCircle(digits);
        if (!mounted) return;
        setState(() {
          _operator = oc.operator;
          _circle = oc.circle;
        });
      } catch (_) {
        // User can pick manually on plans step.
      }
    });
  }

  void _proceedToPlans() {
    final digits = _phoneController.text.replaceAll(RegExp(r'\D'), '');
    if (digits.length != 10) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Enter a 10-digit mobile number')),
      );
      return;
    }
    setState(() {
      _phone = digits;
      _step = _Step.plans;
    });
  }

  void _onPlanPicked(MobilePlan plan) {
    setState(() {
      _selectedPlan = plan;
      _step = _Step.confirm;
    });
  }

  Future<void> _onPay() async {
    final phone = _phone;
    final plan = _selectedPlan;
    if (phone == null || plan == null) return;
    final paymentId = await showBillPaySheet(
      context,
      BillPayRequest(
        providerId: 'recharge_${plan.operator}',
        providerName: plan.operator,
        identifier: phone,
        amountPaise: plan.planAmountPaise,
        categoryId: 'mobile_prepaid',
        allowAmountEdit: false,
        phone: phone,
        operator: plan.operator,
        circle: plan.circle,
        planId: plan.id,
      ),
    );
    if (paymentId != null && mounted) {
      context.pushReplacement('/billpay/payments/$paymentId');
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Mobile recharge', style: AppTextStyles.h2),
        leading: IconButton(
          icon: const Icon(
            Icons.arrow_back_ios_new_rounded,
            color: AppColors.textPrimary,
            size: 18,
          ),
          onPressed: () {
            if (_step == _Step.confirm) {
              setState(() => _step = _Step.plans);
            } else if (_step == _Step.plans) {
              setState(() => _step = _Step.phone);
            } else {
              context.pop();
            }
          },
        ),
      ),
      body: switch (_step) {
        _Step.phone => _buildPhoneStep(),
        _Step.plans => _buildPlansStep(),
        _Step.confirm => _buildConfirmStep(),
      },
    );
  }

  // ─── Step 1: phone ─────────────────────────────────────────────────────

  Widget _buildPhoneStep() {
    return Padding(
      padding: const EdgeInsets.all(AppSpacing.xxl),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text(
            'Mobile number',
            style: AppTextStyles.label.copyWith(color: AppColors.textPrimary),
          ),
          const SizedBox(height: AppSpacing.s),
          TextField(
            controller: _phoneController,
            keyboardType: TextInputType.phone,
            inputFormatters: [
              FilteringTextInputFormatter.digitsOnly,
              LengthLimitingTextInputFormatter(10),
            ],
            onChanged: _onPhoneChanged,
            style: AppTextStyles.h1.copyWith(
              color: AppColors.textPrimary,
              fontSize: 24,
            ),
            decoration: InputDecoration(
              prefixText: '+91 ',
              prefixStyle: AppTextStyles.h1.copyWith(
                color: AppColors.textTertiary,
                fontSize: 24,
              ),
              hintText: '98765 43210',
              hintStyle: AppTextStyles.bodySmall,
              filled: true,
              fillColor: AppColors.bgTertiary,
              border: OutlineInputBorder(
                borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
                borderSide: BorderSide.none,
              ),
              contentPadding: const EdgeInsets.symmetric(
                horizontal: AppSpacing.l,
                vertical: AppSpacing.l,
              ),
            ),
          ),
          if (_operator != null && _circle != null) ...[
            const SizedBox(height: AppSpacing.l),
            Container(
              padding: const EdgeInsets.all(AppSpacing.l),
              decoration: BoxDecoration(
                color: AppColors.bgTertiary,
                borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
                border: Border.all(color: AppColors.borderSubtle),
              ),
              child: Row(
                children: [
                  const Icon(
                    Icons.cell_tower_rounded,
                    color: AppColors.posttubePrimary,
                  ),
                  const SizedBox(width: AppSpacing.l),
                  Expanded(
                    child: Text(
                      'Detected: $_operator · $_circle',
                      style: AppTextStyles.bodyMedium.copyWith(
                        color: AppColors.textPrimary,
                      ),
                    ),
                  ),
                  TextButton(
                    onPressed: () => _showOperatorPicker(),
                    child: Text(
                      'Edit',
                      style: AppTextStyles.bodySmall.copyWith(
                        color: AppColors.postbookPrimary,
                      ),
                    ),
                  ),
                ],
              ),
            ),
          ],
          const SizedBox(height: AppSpacing.xxl),
          ElevatedButton(
            onPressed: _proceedToPlans,
            style: ElevatedButton.styleFrom(
              backgroundColor: AppColors.postbookPrimary,
              padding: const EdgeInsets.symmetric(vertical: 14),
            ),
            child: const Text('See plans'),
          ),
        ],
      ),
    );
  }

  Future<void> _showOperatorPicker() async {
    const operators = ['Jio', 'Airtel', 'Vi', 'BSNL'];
    const circles = [
      'Delhi',
      'Mumbai',
      'Karnataka',
      'Tamil Nadu',
      'Maharashtra',
      'Andhra Pradesh',
    ];
    final picked = await showModalBottomSheet<({String op, String circle})>(
      context: context,
      backgroundColor: AppColors.bgPrimary,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(24)),
      ),
      builder: (ctx) {
        String op = _operator ?? operators.first;
        String circle = _circle ?? circles.first;
        return StatefulBuilder(
          builder: (ctx, setSheet) => Padding(
            padding: const EdgeInsets.all(AppSpacing.xxl),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Text('Pick operator & circle', style: AppTextStyles.h2),
                const SizedBox(height: AppSpacing.l),
                DropdownButtonFormField<String>(
                  initialValue: op,
                  dropdownColor: AppColors.bgTertiary,
                  items: [
                    for (final o in operators)
                      DropdownMenuItem(value: o, child: Text(o)),
                  ],
                  onChanged: (v) {
                    if (v != null) setSheet(() => op = v);
                  },
                ),
                const SizedBox(height: AppSpacing.l),
                DropdownButtonFormField<String>(
                  initialValue: circle,
                  dropdownColor: AppColors.bgTertiary,
                  items: [
                    for (final c in circles)
                      DropdownMenuItem(value: c, child: Text(c)),
                  ],
                  onChanged: (v) {
                    if (v != null) setSheet(() => circle = v);
                  },
                ),
                const SizedBox(height: AppSpacing.xxl),
                ElevatedButton(
                  style: ElevatedButton.styleFrom(
                    backgroundColor: AppColors.postbookPrimary,
                  ),
                  onPressed: () => Navigator.of(ctx).pop(
                    (op: op, circle: circle),
                  ),
                  child: const Text('Apply'),
                ),
              ],
            ),
          ),
        );
      },
    );
    if (picked != null) {
      setState(() {
        _operator = picked.op;
        _circle = picked.circle;
      });
    }
  }

  // ─── Step 2: plans ─────────────────────────────────────────────────────

  Widget _buildPlansStep() {
    if (_operator == null || _circle == null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(AppSpacing.xxl),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Text(
                'Could not auto-detect operator. Pick manually.',
                style: AppTextStyles.bodySmall,
                textAlign: TextAlign.center,
              ),
              const SizedBox(height: AppSpacing.l),
              ElevatedButton(
                onPressed: _showOperatorPicker,
                style: ElevatedButton.styleFrom(
                  backgroundColor: AppColors.postbookPrimary,
                ),
                child: const Text('Pick operator & circle'),
              ),
            ],
          ),
        ),
      );
    }
    return _PlansList(
      operator: _operator!,
      circle: _circle!,
      onPick: _onPlanPicked,
    );
  }

  // ─── Step 3: confirm ───────────────────────────────────────────────────

  Widget _buildConfirmStep() {
    final plan = _selectedPlan!;
    return Padding(
      padding: const EdgeInsets.all(AppSpacing.xxl),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Container(
            padding: const EdgeInsets.all(AppSpacing.xxl),
            decoration: BoxDecoration(
              gradient: AppColors.ctaGradient,
              borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
            ),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  '${plan.operator} · ${plan.circle}',
                  style: AppTextStyles.bodySmall.copyWith(
                    color: Colors.white.withAlpha(220),
                  ),
                ),
                const SizedBox(height: AppSpacing.s),
                Text(
                  formatRupees(plan.planAmountPaise),
                  style: AppTextStyles.h1.copyWith(
                    color: Colors.white,
                    fontSize: 32,
                  ),
                ),
                const SizedBox(height: AppSpacing.s),
                Text(
                  plan.description,
                  style: AppTextStyles.bodyMedium.copyWith(color: Colors.white),
                ),
              ],
            ),
          ),
          const SizedBox(height: AppSpacing.xxl),
          _RowKv(label: 'Phone', value: '+91 ${maskIdentifier(_phone ?? '')}'),
          if (plan.validityDays != null)
            _RowKv(label: 'Validity', value: '${plan.validityDays} days'),
          if (plan.dataGbPerDay != null)
            _RowKv(label: 'Data', value: '${plan.dataGbPerDay} GB/day'),
          if (plan.talktimePaise != null)
            _RowKv(
              label: 'Talktime',
              value: formatRupees(plan.talktimePaise!),
            ),
          if (plan.smsCountPerDay != null)
            _RowKv(label: 'SMS', value: '${plan.smsCountPerDay}/day'),
          const Spacer(),
          ElevatedButton(
            onPressed: _onPay,
            style: ElevatedButton.styleFrom(
              backgroundColor: AppColors.postbookPrimary,
              padding: const EdgeInsets.symmetric(vertical: 14),
            ),
            child: Text('Pay ${formatRupees(plan.planAmountPaise)}'),
          ),
        ],
      ),
    );
  }
}

// ─── Plans list ───────────────────────────────────────────────────────────

class _PlansList extends ConsumerStatefulWidget {
  const _PlansList({
    required this.operator,
    required this.circle,
    required this.onPick,
  });

  final String operator;
  final String circle;
  final void Function(MobilePlan) onPick;

  @override
  ConsumerState<_PlansList> createState() => _PlansListState();
}

class _PlansListState extends ConsumerState<_PlansList>
    with SingleTickerProviderStateMixin {
  late final TabController _tabController;
  String _query = '';
  String _sort = 'price_asc';

  static const _categories = <(String key, String label)>[
    ('unlimited', 'Unlimited'),
    ('data', 'Data'),
    ('talktime', 'Talktime'),
    ('topup', 'Topup'),
    ('roaming', 'Roaming'),
  ];

  @override
  void initState() {
    super.initState();
    _tabController = TabController(length: _categories.length, vsync: this);
  }

  @override
  void dispose() {
    _tabController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final plansAsync = ref.watch(
      mobilePlansProvider(
        OperatorCircleQuery(
          operator: widget.operator,
          circle: widget.circle,
        ),
      ),
    );
    return Column(
      children: [
        Container(
          color: AppColors.bgPrimary,
          child: TabBar(
            controller: _tabController,
            isScrollable: true,
            indicatorColor: AppColors.postbookPrimary,
            labelColor: AppColors.textPrimary,
            unselectedLabelColor: AppColors.textTertiary,
            tabs: [
              for (final c in _categories) Tab(text: c.$2),
            ],
          ),
        ),
        Padding(
          padding: const EdgeInsets.all(AppSpacing.l),
          child: Row(
            children: [
              Expanded(
                child: TextField(
                  onChanged: (v) =>
                      setState(() => _query = v.trim().toLowerCase()),
                  style: AppTextStyles.body.copyWith(
                    color: AppColors.textPrimary,
                  ),
                  decoration: InputDecoration(
                    hintText: 'Search plans',
                    hintStyle: AppTextStyles.bodySmall,
                    prefixIcon: const Icon(
                      Icons.search_rounded,
                      color: AppColors.textTertiary,
                    ),
                    filled: true,
                    fillColor: AppColors.bgTertiary,
                    border: OutlineInputBorder(
                      borderRadius:
                          BorderRadius.circular(AppSpacing.radiusMedium),
                      borderSide: BorderSide.none,
                    ),
                    contentPadding: const EdgeInsets.symmetric(
                      horizontal: AppSpacing.l,
                      vertical: AppSpacing.s,
                    ),
                  ),
                ),
              ),
              const SizedBox(width: AppSpacing.s),
              PopupMenuButton<String>(
                color: AppColors.bgTertiary,
                icon: const Icon(
                  Icons.sort_rounded,
                  color: AppColors.textPrimary,
                ),
                onSelected: (v) => setState(() => _sort = v),
                itemBuilder: (_) => const [
                  PopupMenuItem(
                    value: 'price_asc',
                    child: Text(
                      'Price: low → high',
                      style: TextStyle(color: AppColors.textPrimary),
                    ),
                  ),
                  PopupMenuItem(
                    value: 'price_desc',
                    child: Text(
                      'Price: high → low',
                      style: TextStyle(color: AppColors.textPrimary),
                    ),
                  ),
                  PopupMenuItem(
                    value: 'validity_desc',
                    child: Text(
                      'Validity: longest first',
                      style: TextStyle(color: AppColors.textPrimary),
                    ),
                  ),
                ],
              ),
            ],
          ),
        ),
        Expanded(
          child: plansAsync.when(
            loading: () => const Center(
              child: CircularProgressIndicator(
                color: AppColors.postbookPrimary,
              ),
            ),
            error: (_, _) => Center(
              child: Text(
                'Could not load plans.',
                style: AppTextStyles.bodySmall.copyWith(
                  color: AppColors.statusError,
                ),
              ),
            ),
            data: (all) => TabBarView(
              controller: _tabController,
              children: [
                for (final c in _categories)
                  _PlansTab(
                    plans: _apply(all, c.$1),
                    onPick: widget.onPick,
                  ),
              ],
            ),
          ),
        ),
      ],
    );
  }

  List<MobilePlan> _apply(List<MobilePlan> plans, String category) {
    var out = plans.where((p) => p.category == category).toList();
    if (_query.isNotEmpty) {
      out = out
          .where(
            (p) =>
                p.description.toLowerCase().contains(_query) ||
                p.category.toLowerCase().contains(_query),
          )
          .toList();
    }
    out.sort((a, b) {
      switch (_sort) {
        case 'price_desc':
          return b.planAmountPaise.compareTo(a.planAmountPaise);
        case 'validity_desc':
          return (b.validityDays ?? 0).compareTo(a.validityDays ?? 0);
        case 'price_asc':
        default:
          return a.planAmountPaise.compareTo(b.planAmountPaise);
      }
    });
    return out;
  }
}

class _PlansTab extends StatelessWidget {
  const _PlansTab({required this.plans, required this.onPick});

  final List<MobilePlan> plans;
  final void Function(MobilePlan) onPick;

  @override
  Widget build(BuildContext context) {
    if (plans.isEmpty) {
      return Center(
        child: Text('No plans in this category.', style: AppTextStyles.bodySmall),
      );
    }
    return ListView.separated(
      padding: const EdgeInsets.all(AppSpacing.l),
      itemCount: plans.length,
      separatorBuilder: (_, _) => const SizedBox(height: AppSpacing.s),
      itemBuilder: (_, i) {
        final p = plans[i];
        return InkWell(
          borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
          onTap: () => onPick(p),
          child: Container(
            padding: const EdgeInsets.all(AppSpacing.l),
            decoration: BoxDecoration(
              color: AppColors.bgTertiary,
              borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
              border: Border.all(color: AppColors.borderSubtle),
            ),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Text(
                      formatRupees(p.planAmountPaise),
                      style: AppTextStyles.h2,
                    ),
                    const Spacer(),
                    if (p.validityDays != null)
                      Text(
                        '${p.validityDays} days',
                        style: AppTextStyles.bodySmall.copyWith(
                          color: AppColors.posttubePrimary,
                        ),
                      ),
                  ],
                ),
                const SizedBox(height: AppSpacing.xs),
                Text(p.description, style: AppTextStyles.bodySmall),
              ],
            ),
          ),
        );
      },
    );
  }
}

class _RowKv extends StatelessWidget {
  const _RowKv({required this.label, required this.value});

  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: AppSpacing.s),
      child: Row(
        children: [
          SizedBox(
            width: 90,
            child: Text(label, style: AppTextStyles.bodySmall),
          ),
          Expanded(
            child: Text(
              value,
              style: AppTextStyles.bodyMedium.copyWith(
                color: AppColors.textPrimary,
              ),
            ),
          ),
        ],
      ),
    );
  }
}
