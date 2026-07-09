import 'package:atpost_app/data/repositories/tips_repository.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// One-shot tip composer. Mirrors the web TipComposer modal —
/// quick-amount chips, custom amount, message, daily-cap + charge-
/// failure error surfacing. Returns the [Tip] on success so the
/// caller can show a confirmation toast.
class TipSheet extends ConsumerStatefulWidget {
  final String creatorId;
  final String? creatorName;
  final String? postId;

  const TipSheet({
    super.key,
    required this.creatorId,
    this.creatorName,
    this.postId,
  });

  /// Show as a bottom sheet. Returns the tip on success, null on cancel.
  static Future<Tip?> show(
    BuildContext context, {
    required String creatorId,
    String? creatorName,
    String? postId,
  }) {
    return showModalBottomSheet<Tip>(
      context: context,
      isScrollControlled: true,
      backgroundColor: Colors.transparent,
      builder: (_) => TipSheet(
        creatorId: creatorId,
        creatorName: creatorName,
        postId: postId,
      ),
    );
  }

  @override
  ConsumerState<TipSheet> createState() => _TipSheetState();
}

class _TipSheetState extends ConsumerState<TipSheet> {
  static const _quickAmounts = [
    (label: '₹10', paise: 1000),
    (label: '₹50', paise: 5000),
    (label: '₹100', paise: 10000),
    (label: '₹500', paise: 50000),
  ];
  static const _minPaise = 100;
  static const _maxPaise = 500000;
  static const _messageMax = 250;

  int _selectedPaise = 5000;
  final _customCtrl = TextEditingController();
  final _messageCtrl = TextEditingController();
  bool _sending = false;
  String? _error;

  int get _effectivePaise {
    final custom = _customCtrl.text.trim();
    if (custom.isEmpty) return _selectedPaise;
    final asRupees = double.tryParse(custom) ?? 0;
    return (asRupees * 100).round();
  }

  String? _validate() {
    final p = _effectivePaise;
    if (p < _minPaise) return 'Minimum tip is ₹1';
    if (p > _maxPaise) return 'Maximum tip is ₹5,000';
    if (_messageCtrl.text.length > _messageMax) {
      return 'Message must be under $_messageMax characters';
    }
    return null;
  }

  Future<void> _send() async {
    setState(() => _error = null);
    final v = _validate();
    if (v != null) {
      setState(() => _error = v);
      return;
    }
    setState(() => _sending = true);
    try {
      final tip = await ref.read(tipsRepositoryProvider).send(
        recipientId: widget.creatorId,
        amountPaise: _effectivePaise,
        message: _messageCtrl.text.trim().isEmpty
            ? null
            : _messageCtrl.text.trim(),
        postId: widget.postId,
      );
      if (mounted) Navigator.of(context).pop(tip);
    } on TipError catch (e) {
      if (!mounted) return;
      String msg;
      switch (e.code) {
        case 'DAILY_TIP_CAP_EXCEEDED':
          msg =
              "You've hit the ₹20,000 daily tip limit for this creator. Try again tomorrow.";
          break;
        case 'CHARGE_FAILED':
          msg = 'Wallet charge failed. Top up and try again.';
          break;
        default:
          msg = e.message;
      }
      setState(() {
        _sending = false;
        _error = msg;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _sending = false;
        _error = 'Failed to send tip';
      });
    }
  }

  @override
  void dispose() {
    _customCtrl.dispose();
    _messageCtrl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final viewInsets = MediaQuery.of(context).viewInsets;

    return AnimatedPadding(
      duration: const Duration(milliseconds: 150),
      padding: EdgeInsets.only(bottom: viewInsets.bottom),
      child: Container(
        decoration: BoxDecoration(
          color: theme.colorScheme.surface,
          borderRadius: const BorderRadius.vertical(top: Radius.circular(20)),
        ),
        padding: const EdgeInsets.all(20),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Row(
              children: [
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text('Send a tip', style: theme.textTheme.titleLarge),
                      const SizedBox(height: 4),
                      Text(
                        widget.creatorName != null
                            ? 'to ${widget.creatorName}'
                            : 'Support this creator',
                        style: theme.textTheme.bodySmall?.copyWith(
                          color: theme.hintColor,
                        ),
                      ),
                    ],
                  ),
                ),
                IconButton(
                  onPressed: () => Navigator.of(context).pop(),
                  icon: const Icon(Icons.close),
                ),
              ],
            ),
            const SizedBox(height: 12),
            Row(
              children: _quickAmounts.map((q) {
                final isActive =
                    _customCtrl.text.isEmpty && _selectedPaise == q.paise;
                return Expanded(
                  child: Padding(
                    padding: const EdgeInsets.symmetric(horizontal: 4),
                    child: OutlinedButton(
                      onPressed: () {
                        setState(() {
                          _selectedPaise = q.paise;
                          _customCtrl.clear();
                        });
                      },
                      style: OutlinedButton.styleFrom(
                        backgroundColor: isActive
                            ? theme.colorScheme.primary
                            : null,
                        foregroundColor: isActive
                            ? theme.colorScheme.onPrimary
                            : null,
                      ),
                      child: Text(q.label),
                    ),
                  ),
                );
              }).toList(),
            ),
            const SizedBox(height: 12),
            TextField(
              controller: _customCtrl,
              keyboardType: const TextInputType.numberWithOptions(
                decimal: true,
              ),
              decoration: const InputDecoration(
                labelText: 'Custom amount (₹)',
                hintText: 'e.g. 250',
                border: OutlineInputBorder(),
              ),
              onChanged: (_) => setState(() {}),
            ),
            const SizedBox(height: 12),
            TextField(
              controller: _messageCtrl,
              maxLength: _messageMax,
              maxLines: 3,
              decoration: const InputDecoration(
                labelText: 'Message (optional)',
                border: OutlineInputBorder(),
              ),
            ),
            if (_error != null) ...[
              const SizedBox(height: 4),
              Text(
                _error!,
                style: TextStyle(color: theme.colorScheme.error),
              ),
            ],
            const SizedBox(height: 12),
            Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                Text(
                  'Total: ₹${(_effectivePaise / 100).toStringAsFixed(2)}',
                  style: theme.textTheme.bodyMedium,
                ),
                FilledButton(
                  onPressed: _sending ? null : _send,
                  child: Text(_sending ? 'Sending…' : 'Send tip'),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}
