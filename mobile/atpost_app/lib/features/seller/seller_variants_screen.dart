// Seller variants management — add / edit / archive variants on a
// product. Mirrors the postbook-ui /seller/products/[id]/variants
// page; SKU is pinned after creation (bulk-import merge key), archive
// is soft-delete so existing orders + cart_items keep resolving.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/commerce.dart';
import 'package:atpost_app/data/repositories/commerce_repository.dart';
import 'package:atpost_app/providers/commerce_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class SellerVariantsScreen extends ConsumerWidget {
  const SellerVariantsScreen({super.key, required this.productId});

  final String productId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final variantsAsync = ref.watch(productVariantsProvider(productId));
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Variants', style: AppTextStyles.h2),
      ),
      floatingActionButton: FloatingActionButton.extended(
        backgroundColor: AppColors.postbookPrimary,
        icon: const Icon(Icons.add, color: Colors.white),
        label: const Text('Add variant',
            style: TextStyle(color: Colors.white, fontWeight: FontWeight.w800)),
        onPressed: () => _openEditor(context, productId: productId),
      ),
      body: RefreshIndicator(
        onRefresh: () async {
          ref.invalidate(productVariantsProvider(productId));
          await ref.read(productVariantsProvider(productId).future);
        },
        color: AppColors.postbookPrimary,
        child: variantsAsync.when(
          loading: () => const Center(
            child: CircularProgressIndicator(color: AppColors.postbookPrimary),
          ),
          error: (e, _) => ListView(
            children: [
              SizedBox(
                height: MediaQuery.of(context).size.height * 0.6,
                child: Center(
                  child: Padding(
                    padding: const EdgeInsets.all(AppSpacing.xxl),
                    child: Text(
                      'Could not load variants.\n$e',
                      textAlign: TextAlign.center,
                      style: AppTextStyles.body,
                    ),
                  ),
                ),
              ),
            ],
          ),
          data: (variants) {
            if (variants.isEmpty) {
              return ListView(
                children: [
                  SizedBox(
                    height: MediaQuery.of(context).size.height * 0.6,
                    child: Center(
                      child: Column(
                        mainAxisAlignment: MainAxisAlignment.center,
                        children: [
                          const Icon(Icons.style_outlined,
                              size: 56, color: AppColors.textGhost),
                          const SizedBox(height: AppSpacing.l),
                          Text('No variants yet', style: AppTextStyles.h2),
                          const SizedBox(height: AppSpacing.s),
                          Padding(
                            padding: const EdgeInsets.symmetric(
                                horizontal: AppSpacing.xxl),
                            child: Text(
                              'Add a variant for each SKU you want to sell under this product.',
                              textAlign: TextAlign.center,
                              style: AppTextStyles.body,
                            ),
                          ),
                        ],
                      ),
                    ),
                  ),
                ],
              );
            }
            return ListView.separated(
              padding: const EdgeInsets.fromLTRB(
                AppSpacing.l,
                AppSpacing.l,
                AppSpacing.l,
                80, // breathing room above the FAB
              ),
              itemCount: variants.length,
              separatorBuilder: (_, _) => const SizedBox(height: AppSpacing.s),
              itemBuilder: (_, i) => _VariantRow(
                variant: variants[i],
                productId: productId,
              ),
            );
          },
        ),
      ),
    );
  }
}

class _VariantRow extends ConsumerWidget {
  const _VariantRow({required this.variant, required this.productId});

  final ProductVariantDetail variant;
  final String productId;

  Future<void> _archive(BuildContext context, WidgetRef ref) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text('Archive ${variant.sku}?', style: AppTextStyles.h3),
        content: Text(
          "Existing orders keep working, but customers can't add new units.",
          style: AppTextStyles.body,
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: const Text('Cancel'),
          ),
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(true),
            child: const Text('Archive'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;
    try {
      await ref.read(commerceRepositoryProvider).archiveProductVariant(variant.id);
      ref.invalidate(productVariantsProvider(productId));
      if (!context.mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('${variant.sku} archived')),
      );
    } catch (e) {
      if (!context.mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Could not archive: $e')),
      );
    }
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final archived = variant.status == 'archived';
    return Container(
      padding: const EdgeInsets.all(AppSpacing.l),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  variant.sku,
                  style: AppTextStyles.label.copyWith(
                    fontFamily: 'monospace',
                    color: archived ? AppColors.textMuted : AppColors.textPrimary,
                  ),
                ),
              ),
              _StatusChip(status: variant.status),
            ],
          ),
          if (variant.optionsLabel != '—') ...[
            const SizedBox(height: 2),
            Text(variant.optionsLabel, style: AppTextStyles.bodySmall),
          ],
          const SizedBox(height: AppSpacing.s),
          Row(
            children: [
              Text(
                'Rs. ${variant.sellingPrice.toStringAsFixed(0)}',
                style: AppTextStyles.h3,
              ),
              const SizedBox(width: AppSpacing.s),
              if (variant.mrp > variant.sellingPrice)
                Text(
                  'Rs. ${variant.mrp.toStringAsFixed(0)}',
                  style: AppTextStyles.bodySmall.copyWith(
                    decoration: TextDecoration.lineThrough,
                    color: AppColors.textMuted,
                  ),
                ),
              const Spacer(),
              if (!archived)
                TextButton(
                  onPressed: () => _openEditor(
                    context,
                    productId: productId,
                    existing: variant,
                  ),
                  child: const Text('Edit'),
                ),
              if (!archived)
                TextButton(
                  onPressed: () => _archive(context, ref),
                  style: TextButton.styleFrom(
                    foregroundColor: const Color(0xFFB91C1C),
                  ),
                  child: const Text('Archive'),
                ),
            ],
          ),
        ],
      ),
    );
  }
}

class _StatusChip extends StatelessWidget {
  const _StatusChip({required this.status});

  final String status;

  @override
  Widget build(BuildContext context) {
    final (bg, fg) = switch (status) {
      'active' => (const Color(0xFFD1FAE5), const Color(0xFF047857)),
      'archived' => (AppColors.bgSecondary, AppColors.textMuted),
      _ => (const Color(0xFFFEF3C7), const Color(0xFF92400E)),
    };
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(999),
      ),
      child: Text(
        status.toUpperCase(),
        style: AppTextStyles.labelTiny.copyWith(
          color: fg,
          fontWeight: FontWeight.w800,
        ),
      ),
    );
  }
}

/// _openEditor pushes the variant create/edit sheet. existing=null →
/// create mode; non-null → edit. The sheet handles the actual repo
/// call so this caller doesn't need to know about the wire shape.
void _openEditor(
  BuildContext context, {
  required String productId,
  ProductVariantDetail? existing,
}) {
  showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    backgroundColor: AppColors.bgPrimary,
    shape: const RoundedRectangleBorder(
      borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
    ),
    builder: (_) => _VariantEditorSheet(
      productId: productId,
      existing: existing,
    ),
  );
}

class _VariantEditorSheet extends ConsumerStatefulWidget {
  const _VariantEditorSheet({required this.productId, this.existing});

  final String productId;
  final ProductVariantDetail? existing;

  @override
  ConsumerState<_VariantEditorSheet> createState() =>
      _VariantEditorSheetState();
}

class _VariantEditorSheetState extends ConsumerState<_VariantEditorSheet> {
  late final TextEditingController _sku;
  late final TextEditingController _barcode;
  late final TextEditingController _opt1Name;
  late final TextEditingController _opt1Value;
  late final TextEditingController _opt2Name;
  late final TextEditingController _opt2Value;
  late final TextEditingController _mrp;
  late final TextEditingController _sellingPrice;
  late final TextEditingController _costPrice;
  late final TextEditingController _weight;

  bool _busy = false;
  String? _error;

  bool get _isEdit => widget.existing != null;

  @override
  void initState() {
    super.initState();
    final e = widget.existing;
    _sku = TextEditingController(text: e?.sku ?? '');
    _barcode = TextEditingController(text: e?.barcode ?? '');
    _opt1Name = TextEditingController(text: e?.option1Name ?? 'size');
    _opt1Value = TextEditingController(text: e?.option1Value ?? '');
    _opt2Name = TextEditingController(text: e?.option2Name ?? '');
    _opt2Value = TextEditingController(text: e?.option2Value ?? '');
    _mrp = TextEditingController(text: e?.mrp.toString() ?? '');
    _sellingPrice = TextEditingController(text: e?.sellingPrice.toString() ?? '');
    _costPrice = TextEditingController(text: e?.costPrice?.toString() ?? '');
    _weight = TextEditingController(text: e?.weightGrams?.toString() ?? '');
  }

  @override
  void dispose() {
    for (final c in [
      _sku, _barcode, _opt1Name, _opt1Value, _opt2Name, _opt2Value,
      _mrp, _sellingPrice, _costPrice, _weight,
    ]) {
      c.dispose();
    }
    super.dispose();
  }

  Future<void> _submit() async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final sku = _sku.text.trim();
      final mrp = double.tryParse(_mrp.text.trim()) ?? 0;
      final sp = double.tryParse(_sellingPrice.text.trim()) ?? 0;
      if (sku.isEmpty) {
        setState(() => _error = 'SKU is required.');
        return;
      }
      if (mrp <= 0 || sp <= 0) {
        setState(() => _error = 'MRP and selling price must be positive.');
        return;
      }
      if (sp > mrp) {
        setState(() => _error = 'Selling price cannot exceed MRP.');
        return;
      }
      final repo = ref.read(commerceRepositoryProvider);
      if (_isEdit) {
        // Sparse patch — only fields that actually have values.
        final patch = <String, dynamic>{
          'mrp': mrp,
          'selling_price': sp,
        };
        _setIfNotEmpty(patch, 'barcode', _barcode.text);
        _setIfNotEmpty(patch, 'option_1_name', _opt1Name.text);
        _setIfNotEmpty(patch, 'option_1_value', _opt1Value.text);
        _setIfNotEmpty(patch, 'option_2_name', _opt2Name.text);
        _setIfNotEmpty(patch, 'option_2_value', _opt2Value.text);
        final cost = double.tryParse(_costPrice.text.trim());
        if (cost != null) patch['cost_price'] = cost;
        final weight = int.tryParse(_weight.text.trim());
        if (weight != null) patch['weight_grams'] = weight;
        await repo.updateProductVariant(widget.existing!.id, patch);
      } else {
        final input = CreateVariantInput(
          sku: sku,
          mrp: mrp,
          sellingPrice: sp,
          barcode: _emptyToNull(_barcode.text),
          option1Name: _emptyToNull(_opt1Name.text),
          option1Value: _emptyToNull(_opt1Value.text),
          option2Name: _emptyToNull(_opt2Name.text),
          option2Value: _emptyToNull(_opt2Value.text),
          costPrice: double.tryParse(_costPrice.text.trim()),
          weightGrams: int.tryParse(_weight.text.trim()),
        );
        await repo.addProductVariant(widget.productId, input);
      }
      ref.invalidate(productVariantsProvider(widget.productId));
      if (!mounted) return;
      Navigator.of(context).pop();
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(_isEdit ? 'Variant updated' : 'Variant created')),
      );
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = 'Could not save: $e');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  void _setIfNotEmpty(Map<String, dynamic> map, String key, String value) {
    final trimmed = value.trim();
    if (trimmed.isNotEmpty) map[key] = trimmed;
  }

  String? _emptyToNull(String value) {
    final t = value.trim();
    return t.isEmpty ? null : t;
  }

  @override
  Widget build(BuildContext context) {
    final bottom = MediaQuery.of(context).viewInsets.bottom;
    return Padding(
      padding: EdgeInsets.only(
        left: AppSpacing.l,
        right: AppSpacing.l,
        top: AppSpacing.l,
        bottom: bottom + AppSpacing.l,
      ),
      child: SingleChildScrollView(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          mainAxisSize: MainAxisSize.min,
          children: [
            Center(
              child: Container(
                width: 36,
                height: 4,
                decoration: BoxDecoration(
                  color: AppColors.borderSubtle,
                  borderRadius: BorderRadius.circular(2),
                ),
              ),
            ),
            const SizedBox(height: AppSpacing.l),
            Text(
              _isEdit ? 'Edit variant' : 'New variant',
              style: AppTextStyles.h2,
            ),
            const SizedBox(height: AppSpacing.l),
            _Field(
              label: 'SKU',
              controller: _sku,
              readOnly: _isEdit,
              hint: _isEdit ? 'SKU is fixed after creation' : null,
            ),
            _Field(label: 'Barcode (optional)', controller: _barcode),
            Row(
              children: [
                Expanded(child: _Field(label: 'Option 1 name', controller: _opt1Name)),
                const SizedBox(width: AppSpacing.s),
                Expanded(child: _Field(label: 'Value', controller: _opt1Value)),
              ],
            ),
            Row(
              children: [
                Expanded(child: _Field(label: 'Option 2 name', controller: _opt2Name)),
                const SizedBox(width: AppSpacing.s),
                Expanded(child: _Field(label: 'Value', controller: _opt2Value)),
              ],
            ),
            Row(
              children: [
                Expanded(child: _Field(label: 'MRP', controller: _mrp, numeric: true)),
                const SizedBox(width: AppSpacing.s),
                Expanded(
                  child: _Field(label: 'Selling price', controller: _sellingPrice, numeric: true),
                ),
              ],
            ),
            Row(
              children: [
                Expanded(child: _Field(label: 'Cost', controller: _costPrice, numeric: true)),
                const SizedBox(width: AppSpacing.s),
                Expanded(child: _Field(label: 'Weight (g)', controller: _weight, numeric: true)),
              ],
            ),
            if (_error != null) ...[
              const SizedBox(height: AppSpacing.s),
              Container(
                padding: const EdgeInsets.all(AppSpacing.s),
                decoration: BoxDecoration(
                  color: const Color(0xFFFFE4E6),
                  borderRadius: BorderRadius.circular(AppSpacing.radiusSmall),
                ),
                child: Text(
                  _error!,
                  style: AppTextStyles.bodySmall.copyWith(
                    color: const Color(0xFFB91C1C),
                  ),
                ),
              ),
            ],
            const SizedBox(height: AppSpacing.l),
            ElevatedButton(
              style: ElevatedButton.styleFrom(
                backgroundColor: AppColors.postbookPrimary,
                padding: const EdgeInsets.symmetric(vertical: 14),
              ),
              onPressed: _busy ? null : _submit,
              child: _busy
                  ? const SizedBox(
                      width: 18,
                      height: 18,
                      child: CircularProgressIndicator(
                          strokeWidth: 2, color: Colors.white),
                    )
                  : Text(_isEdit ? 'Save changes' : 'Create variant',
                      style: const TextStyle(color: Colors.white)),
            ),
          ],
        ),
      ),
    );
  }
}

class _Field extends StatelessWidget {
  const _Field({
    required this.label,
    required this.controller,
    this.numeric = false,
    this.readOnly = false,
    this.hint,
  });

  final String label;
  final TextEditingController controller;
  final bool numeric;
  final bool readOnly;
  final String? hint;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: AppSpacing.s),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(label, style: AppTextStyles.labelTiny),
          const SizedBox(height: 4),
          TextField(
            controller: controller,
            readOnly: readOnly,
            keyboardType: numeric
                ? const TextInputType.numberWithOptions(decimal: true)
                : TextInputType.text,
            style: AppTextStyles.body,
            decoration: InputDecoration(
              isDense: true,
              filled: true,
              fillColor: readOnly ? AppColors.bgSecondary : AppColors.bgCard,
              contentPadding: const EdgeInsets.symmetric(
                  horizontal: 10, vertical: 10),
              border: OutlineInputBorder(
                borderRadius: BorderRadius.circular(AppSpacing.radiusSmall),
                borderSide: BorderSide(color: AppColors.borderSubtle),
              ),
              enabledBorder: OutlineInputBorder(
                borderRadius: BorderRadius.circular(AppSpacing.radiusSmall),
                borderSide: BorderSide(color: AppColors.borderSubtle),
              ),
              focusedBorder: OutlineInputBorder(
                borderRadius: BorderRadius.circular(AppSpacing.radiusSmall),
                borderSide: BorderSide(color: AppColors.postbookPrimary, width: 1.5),
              ),
              hintText: hint,
              hintStyle: AppTextStyles.bodySmall.copyWith(
                color: AppColors.textMuted,
              ),
            ),
          ),
        ],
      ),
    );
  }
}
