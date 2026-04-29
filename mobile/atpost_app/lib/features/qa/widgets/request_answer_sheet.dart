import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'package:atpost_app/data/repositories/qa_repository.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';

/// Bottom sheet that lets the viewer pick a user to invite to answer a
/// question. Uses the existing `/v1/search/users` endpoint with debounced
/// typeahead — no free-text UUID prompts.
class RequestAnswerSheet extends ConsumerStatefulWidget {
  const RequestAnswerSheet({super.key, required this.questionId});

  final String questionId;

  static Future<bool?> show(BuildContext context, {required String questionId}) {
    return showModalBottomSheet<bool>(
      context: context,
      isScrollControlled: true,
      useSafeArea: true,
      backgroundColor: Theme.of(context).colorScheme.surface,
      builder: (_) => RequestAnswerSheet(questionId: questionId),
    );
  }

  @override
  ConsumerState<RequestAnswerSheet> createState() =>
      _RequestAnswerSheetState();
}

class _RequestAnswerSheetState extends ConsumerState<RequestAnswerSheet> {
  final _controller = TextEditingController();
  Timer? _debounce;

  String _query = '';
  bool _searching = false;
  bool _submitting = false;
  String? _submittingUserId;
  Object? _searchError;
  List<_PickerEntry> _results = const [];

  @override
  void dispose() {
    _debounce?.cancel();
    _controller.dispose();
    super.dispose();
  }

  void _onChanged(String raw) {
    final q = raw.trim();
    _debounce?.cancel();
    if (q == _query) return;
    if (q.length < 2) {
      setState(() {
        _query = q;
        _results = const [];
        _searchError = null;
      });
      return;
    }
    _debounce = Timer(const Duration(milliseconds: 350), () => _runSearch(q));
  }

  Future<void> _runSearch(String q) async {
    setState(() {
      _query = q;
      _searching = true;
      _searchError = null;
    });
    try {
      final repo = ref.read(userRepositoryProvider);
      final result = await repo.searchUsers(q, limit: 10);
      if (!mounted || _query != q) return;
      setState(() {
        _results = result.users
            .map((u) => _PickerEntry(
                  id: u.id,
                  name: u.displayName.isNotEmpty
                      ? u.displayName
                      : (u.username.isNotEmpty ? u.username : u.id),
                  username: u.username,
                  avatarUrl: u.avatarUrl,
                ))
            .toList();
      });
    } catch (e) {
      if (!mounted || _query != q) return;
      setState(() => _searchError = e);
    } finally {
      if (mounted) setState(() => _searching = false);
    }
  }

  Future<void> _submit(_PickerEntry entry) async {
    if (_submitting) return;
    setState(() {
      _submitting = true;
      _submittingUserId = entry.id;
    });
    try {
      await ref
          .read(qaRepositoryProvider)
          .requestAnswer(widget.questionId, entry.id);
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Asked ${entry.name} to answer.')),
      );
      Navigator.of(context).pop(true);
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Could not send request: $e')),
      );
    } finally {
      if (mounted) {
        setState(() {
          _submitting = false;
          _submittingUserId = null;
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final mq = MediaQuery.of(context);

    return Padding(
      padding: EdgeInsets.only(bottom: mq.viewInsets.bottom),
      child: SafeArea(
        top: false,
        child: ConstrainedBox(
          constraints: BoxConstraints(maxHeight: mq.size.height * 0.75),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const SizedBox(height: 8),
              Container(
                width: 36,
                height: 4,
                decoration: BoxDecoration(
                  color: theme.dividerColor,
                  borderRadius: BorderRadius.circular(2),
                ),
              ),
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 12, 16, 0),
                child: Row(
                  children: [
                    Text(
                      'Ask someone to answer',
                      style: theme.textTheme.titleMedium,
                    ),
                    const Spacer(),
                    IconButton(
                      icon: const Icon(Icons.close),
                      onPressed: () => Navigator.of(context).pop(false),
                    ),
                  ],
                ),
              ),
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 8, 16, 8),
                child: TextField(
                  controller: _controller,
                  autofocus: true,
                  decoration: const InputDecoration(
                    prefixIcon: Icon(Icons.search),
                    hintText: 'Search by name or @username',
                    border: OutlineInputBorder(),
                    isDense: true,
                  ),
                  onChanged: _onChanged,
                ),
              ),
              Expanded(child: _buildResults(theme)),
            ],
          ),
        ),
      ),
    );
  }

  Widget _buildResults(ThemeData theme) {
    if (_query.length < 2) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            'Type at least 2 characters to search.',
            style: theme.textTheme.bodyMedium?.copyWith(
              color: theme.hintColor,
            ),
            textAlign: TextAlign.center,
          ),
        ),
      );
    }
    if (_searching && _results.isEmpty) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_searchError != null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            'Search failed: $_searchError',
            style: theme.textTheme.bodyMedium,
            textAlign: TextAlign.center,
          ),
        ),
      );
    }
    if (_results.isEmpty) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            'No users found for "$_query".',
            style: theme.textTheme.bodyMedium,
            textAlign: TextAlign.center,
          ),
        ),
      );
    }
    return ListView.separated(
      itemCount: _results.length,
      separatorBuilder: (_, _) => const Divider(height: 1),
      itemBuilder: (context, index) {
        final e = _results[index];
        final isThis = _submittingUserId == e.id;
        return ListTile(
          leading: CircleAvatar(
            backgroundImage:
                e.avatarUrl.isNotEmpty ? NetworkImage(e.avatarUrl) : null,
            child: e.avatarUrl.isEmpty
                ? Text(e.name.isNotEmpty ? e.name[0].toUpperCase() : '?')
                : null,
          ),
          title: Text(e.name),
          subtitle:
              e.username.isNotEmpty ? Text('@${e.username}') : null,
          trailing: isThis
              ? const SizedBox(
                  width: 18,
                  height: 18,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : const Icon(Icons.send_rounded),
          onTap: _submitting ? null : () => _submit(e),
        );
      },
    );
  }
}

class _PickerEntry {
  final String id;
  final String name;
  final String username;
  final String avatarUrl;
  _PickerEntry({
    required this.id,
    required this.name,
    required this.username,
    required this.avatarUrl,
  });
}
