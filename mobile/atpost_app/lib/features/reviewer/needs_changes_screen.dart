// Creator-facing "needs changes" screen — shows the super-admin's notes for a
// video that needs edits, and lets the creator re-submit after editing (loops
// back into review).
import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/repositories/reviewer_repository.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class NeedsChangesScreen extends ConsumerStatefulWidget {
  const NeedsChangesScreen({super.key, required this.contentId});
  final String contentId;

  @override
  ConsumerState<NeedsChangesScreen> createState() => _NeedsChangesScreenState();
}

class _NeedsChangesScreenState extends ConsumerState<NeedsChangesScreen> {
  ReviewFeedback? _fb;
  bool _loading = true;
  bool _resubmitting = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() => _loading = true);
    final fb = await ref.read(reviewerRepositoryProvider).feedback(widget.contentId);
    if (!mounted) return;
    setState(() {
      _fb = fb;
      _loading = false;
    });
  }

  Future<void> _resubmit() async {
    setState(() => _resubmitting = true);
    try {
      await ref.read(reviewerRepositoryProvider).resubmit(widget.contentId);
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Re-submitted for review.')),
      );
      Navigator.of(context).maybePop();
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not re-submit. Try again.')),
      );
    } finally {
      if (mounted) setState(() => _resubmitting = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final fb = _fb;
    final notes = fb?.adminNotes?.trim();
    final isRejected = fb?.adminDecision == 'reject';
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(title: const Text('Review feedback'), backgroundColor: AppColors.bgPrimary),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : Padding(
              padding: const EdgeInsets.all(20),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      Icon(
                        isRejected ? Icons.cancel_outlined : Icons.edit_note_rounded,
                        color: isRejected ? const Color(0xFFFF4D4F) : AppColors.statusWarning,
                      ),
                      const SizedBox(width: 8),
                      Text(
                        isRejected ? 'Rejected' : 'Changes requested',
                        style: AppTextStyles.h2.copyWith(fontSize: 20),
                      ),
                    ],
                  ),
                  const SizedBox(height: 16),
                  Container(
                    width: double.infinity,
                    padding: const EdgeInsets.all(14),
                    decoration: BoxDecoration(
                      color: AppColors.bgSecondary,
                      borderRadius: BorderRadius.circular(12),
                      border: Border.all(color: AppColors.borderSubtle),
                    ),
                    child: Text(
                      (notes == null || notes.isEmpty)
                          ? (fb == null ? 'No feedback found for this video.' : 'No notes provided.')
                          : notes,
                      style: AppTextStyles.body.copyWith(color: AppColors.textPrimary),
                    ),
                  ),
                  const Spacer(),
                  if (!isRejected && fb != null)
                    SizedBox(
                      width: double.infinity,
                      child: FilledButton(
                        onPressed: _resubmitting ? null : _resubmit,
                        style: FilledButton.styleFrom(
                          padding: const EdgeInsets.symmetric(vertical: 14),
                        ),
                        child: Text(_resubmitting ? 'Re-submitting…' : 'I edited it — re-submit'),
                      ),
                    ),
                ],
              ),
            ),
    );
  }
}
