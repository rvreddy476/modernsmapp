import 'dart:async';

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/data/repositories/pulse_repository.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:atpost_app/providers/pulse_safety_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Sprint 4 — vouch request bottom sheet.
///
/// Use:
/// ```
/// showModalBottomSheet(
///   context: context,
///   isScrollControlled: true,
///   builder: (_) => const SendVouchSheet(),
/// );
/// ```
class SendVouchSheet extends ConsumerStatefulWidget {
  const SendVouchSheet({super.key});

  @override
  ConsumerState<SendVouchSheet> createState() => _SendVouchSheetState();
}

class _SendVouchSheetState extends ConsumerState<SendVouchSheet> {
  final _searchController = TextEditingController();
  final _noteController = TextEditingController();
  final _communityController = TextEditingController();

  Timer? _debounce;
  String _query = '';
  bool _searching = false;
  List<User> _results = const [];

  User? _selectedUser;
  String _relationship = 'friend';
  bool _submitting = false;
  String? _error;

  static const Map<String, String> _relationshipOptions = {
    'friend': 'Friend',
    'community_member': 'Community member',
    'colleague': 'Colleague',
    'family': 'Family',
  };

  @override
  void dispose() {
    _debounce?.cancel();
    _searchController.dispose();
    _noteController.dispose();
    _communityController.dispose();
    super.dispose();
  }

  void _onQueryChanged(String value) {
    _debounce?.cancel();
    _debounce = Timer(const Duration(milliseconds: 350), () {
      _runSearch(value);
    });
  }

  Future<void> _runSearch(String value) async {
    final trimmed = value.trim();
    if (trimmed.length < 2) {
      setState(() {
        _query = trimmed;
        _results = const [];
      });
      return;
    }
    setState(() {
      _query = trimmed;
      _searching = true;
    });
    try {
      final repo = ref.read(userRepositoryProvider);
      final result = await repo.searchUsers(trimmed, limit: 10);
      if (!mounted) return;
      setState(() {
        _results = result.users;
        _searching = false;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _searching = false;
        _results = const [];
      });
    }
  }

  Future<void> _send() async {
    final user = _selectedUser;
    if (user == null) {
      setState(() => _error = 'Pick someone first.');
      return;
    }
    setState(() {
      _submitting = true;
      _error = null;
    });
    try {
      final repo = ref.read(pulseRepositoryProvider);
      await repo.sendVouchRequest(
        voucheeId: user.id,
        relationship: _relationship,
        communityId: _relationship == 'community_member'
            ? _communityController.text.trim()
            : null,
        note: _noteController.text.trim(),
      );
      if (!mounted) return;
      ref.invalidate(vouchesSentProvider);
      Navigator.of(context).pop(true);
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Vouch request sent.')),
      );
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _submitting = false;
        _error = 'Could not send the vouch request. Please try again.';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final viewInsets = MediaQuery.of(context).viewInsets;
    return Padding(
      padding: EdgeInsets.only(bottom: viewInsets.bottom),
      child: Container(
        padding: const EdgeInsets.fromLTRB(18, 14, 18, 22),
        decoration: const BoxDecoration(
          color: AppColors.bgSecondary,
          borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
        ),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Center(
              child: Container(
                width: 44,
                height: 4,
                margin: const EdgeInsets.only(bottom: 14),
                decoration: BoxDecoration(
                  color: AppColors.borderMedium,
                  borderRadius: BorderRadius.circular(AppSpacing.radiusFull),
                ),
              ),
            ),
            Text('Ask someone to vouch for you', style: AppTextStyles.h2),
            const SizedBox(height: 6),
            Text(
              'A vouch is a public co-sign from someone you trust. They '
              'pick a relationship and add an optional note (140 chars).',
              style: AppTextStyles.bodySmall,
            ),
            const SizedBox(height: 14),
            _searchField(),
            const SizedBox(height: 8),
            if (_selectedUser != null) _selectedUserChip(),
            if (_selectedUser == null && _query.length >= 2)
              _resultsList(),
            const SizedBox(height: 14),
            Text('Relationship', style: AppTextStyles.label),
            const SizedBox(height: 6),
            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: _relationshipOptions.entries
                  .map((e) => ChoiceChip(
                        label: Text(e.value),
                        selected: _relationship == e.key,
                        onSelected: (_) =>
                            setState(() => _relationship = e.key),
                      ))
                  .toList(),
            ),
            if (_relationship == 'community_member') ...[
              const SizedBox(height: 12),
              TextField(
                controller: _communityController,
                style: AppTextStyles.body
                    .copyWith(color: AppColors.textPrimary),
                decoration: const InputDecoration(
                  hintText: 'Community ID or slug',
                ),
              ),
            ],
            const SizedBox(height: 12),
            TextField(
              controller: _noteController,
              maxLength: 140,
              maxLines: 3,
              style: AppTextStyles.body
                  .copyWith(color: AppColors.textPrimary),
              decoration: const InputDecoration(
                hintText: 'Optional note (max 140 chars)',
                counterText: '',
              ),
            ),
            const SizedBox(height: 8),
            if (_error != null)
              Text(_error!,
                  style: AppTextStyles.bodySmall
                      .copyWith(color: AppColors.statusError)),
            const SizedBox(height: 12),
            SizedBox(
              width: double.infinity,
              child: FilledButton(
                onPressed: _submitting ? null : _send,
                style: FilledButton.styleFrom(
                  backgroundColor: AppColors.postbookPrimary,
                  minimumSize: const Size.fromHeight(48),
                  shape: RoundedRectangleBorder(
                    borderRadius:
                        BorderRadius.circular(AppSpacing.radiusLarge),
                  ),
                ),
                child: _submitting
                    ? const SizedBox(
                        width: 18,
                        height: 18,
                        child: CircularProgressIndicator(
                          strokeWidth: 2,
                          color: Colors.white,
                        ),
                      )
                    : Text('Send vouch request',
                        style:
                            AppTextStyles.h3.copyWith(color: Colors.white)),
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _searchField() {
    return TextField(
      controller: _searchController,
      onChanged: _onQueryChanged,
      style: AppTextStyles.body.copyWith(color: AppColors.textPrimary),
      decoration: InputDecoration(
        hintText: 'Search by name or username',
        prefixIcon: const Icon(Icons.search,
            size: 18, color: AppColors.textTertiary),
        suffixIcon: _searching
            ? const Padding(
                padding: EdgeInsets.all(12),
                child: SizedBox(
                  width: 14,
                  height: 14,
                  child: CircularProgressIndicator(strokeWidth: 2),
                ),
              )
            : null,
      ),
    );
  }

  Widget _selectedUserChip() {
    final user = _selectedUser!;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      decoration: BoxDecoration(
        color: AppColors.bgCard,
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: Row(
        children: [
          CircleAvatar(
            radius: 16,
            backgroundColor: AppColors.bgCardHover,
            child: Text(
              user.displayName.isNotEmpty
                  ? user.displayName[0].toUpperCase()
                  : '?',
              style: AppTextStyles.label,
            ),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(user.displayName, style: AppTextStyles.h3),
                Text('@${user.username}',
                    style: AppTextStyles.bodySmall),
              ],
            ),
          ),
          IconButton(
            onPressed: () => setState(() => _selectedUser = null),
            icon: const Icon(Icons.close, size: 18),
          ),
        ],
      ),
    );
  }

  Widget _resultsList() {
    if (_results.isEmpty && !_searching) {
      return Padding(
        padding: const EdgeInsets.symmetric(vertical: 12),
        child: Text('No matches.', style: AppTextStyles.bodySmall),
      );
    }
    return ConstrainedBox(
      constraints: const BoxConstraints(maxHeight: 220),
      child: ListView.separated(
        shrinkWrap: true,
        itemCount: _results.length,
        separatorBuilder: (_, _) => const SizedBox(height: 6),
        itemBuilder: (context, index) {
          final user = _results[index];
          return InkWell(
            borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
            onTap: () => setState(() => _selectedUser = user),
            child: Container(
              padding: const EdgeInsets.symmetric(
                  horizontal: 12, vertical: 10),
              decoration: BoxDecoration(
                color: AppColors.bgCard,
                borderRadius:
                    BorderRadius.circular(AppSpacing.radiusMedium),
              ),
              child: Row(
                children: [
                  CircleAvatar(
                    radius: 16,
                    backgroundColor: AppColors.bgCardHover,
                    child: Text(
                      user.displayName.isNotEmpty
                          ? user.displayName[0].toUpperCase()
                          : '?',
                      style: AppTextStyles.label,
                    ),
                  ),
                  const SizedBox(width: 10),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(user.displayName, style: AppTextStyles.h3),
                        Text('@${user.username}',
                            style: AppTextStyles.bodySmall),
                      ],
                    ),
                  ),
                ],
              ),
            ),
          );
        },
      ),
    );
  }
}
