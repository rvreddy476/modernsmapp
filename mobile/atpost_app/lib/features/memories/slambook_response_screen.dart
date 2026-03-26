import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/slambook.dart';
import 'package:atpost_app/data/repositories/memories_repository.dart';
import 'package:atpost_app/features/memories/slambook_data.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class SlambookResponseScreen extends ConsumerStatefulWidget {
  const SlambookResponseScreen({
    super.key,
    required this.slambookId,
    this.shareToken,
  });

  final String slambookId;
  final String? shareToken;

  @override
  ConsumerState<SlambookResponseScreen> createState() => _SlambookResponseScreenState();
}

class _SlambookResponseScreenState extends ConsumerState<SlambookResponseScreen> {
  final _displayNameController = TextEditingController();
  final Map<String, TextEditingController> _controllers =
      <String, TextEditingController>{};
  bool _anonymous = false;
  bool _saving = false;
  String? _hydratedDetailKey;
  bool _hydrationQueued = false;

  @override
  void dispose() {
    _displayNameController.dispose();
    for (final controller in _controllers.values) {
      controller.dispose();
    }
    super.dispose();
  }

  String _detailHydrationKey(SlambookDetail detail) {
    final sessionId = detail.viewerSession?.id;
    if (sessionId != null && sessionId.isNotEmpty) {
      return 'session:$sessionId';
    }
    return 'draft:${detail.cards.map((card) => card.id).join(",")}';
  }

  void _hydrateFromDetail(SlambookDetail detail) {
    final session = detail.viewerSession;
    final hydrationKey = _detailHydrationKey(detail);
    if (_hydratedDetailKey == hydrationKey || _hydrationQueued) {
      return;
    }

    _hydrationQueued = true;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      if (_hydratedDetailKey == hydrationKey) {
        _hydrationQueued = false;
        return;
      }

      setState(() {
        if (session == null) {
          _anonymous = false;
        } else {
          _anonymous = session.identityMode != 'named';
          final displayName = session.displayName?.trim();
          if (displayName != null && displayName.isNotEmpty) {
            _displayNameController.text = displayName;
          }
        }
        for (final card in detail.cards) {
          final controller = _controllers.putIfAbsent(
            card.id,
            () => TextEditingController(),
          );
          final matchingItems = session?.items
                  .where((item) => item.cardId == card.id)
                  .toList() ??
              const <SlambookResponseItem>[];
          controller.text = matchingItems.isEmpty
              ? ''
              : slambookAnswerPreview(matchingItems.first);
        }
        _hydratedDetailKey = hydrationKey;
        _hydrationQueued = false;
      });
    });
  }

  TextEditingController _controllerFor(String cardId) {
    return _controllers[cardId]!;
  }

  Future<void> _save(SlambookDetail detail, {required bool submit}) async {
    if (_saving) return;
    setState(() => _saving = true);
    try {
      final answers = detail.cards
          .map(
            (card) => SlambookResponseAnswerDraft(
              cardId: card.id,
              answerText: _controllers[card.id]?.text.trim() ?? '',
            ),
          )
          .toList();
      await ref.read(memoriesRepositoryProvider).saveSlambookResponse(
            widget.slambookId,
            displayName: _displayNameController.text.trim(),
            anonymous: _anonymous,
            shareToken: widget.shareToken,
            submit: submit,
            answers: answers,
          );
      ref.invalidate(mySlambooksProvider);
      ref.invalidate(slambookDetailProvider(widget.slambookId));
      ref.invalidate(slambookOpinionSpaceProvider(widget.slambookId));
      ref.invalidate(slambookModerationQueueProvider(widget.slambookId));
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(
            submit ? 'Response submitted.' : 'Draft saved.',
          ),
        ),
      );
      Navigator.of(context).pop();
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(
            submit ? 'Could not submit your response.' : 'Could not save the draft.',
          ),
        ),
      );
    } finally {
      if (mounted) {
        setState(() => _saving = false);
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final detailAsync = widget.shareToken == null
        ? ref.watch(slambookDetailProvider(widget.slambookId))
        : ref.watch(slambookShareDetailProvider(widget.shareToken!));

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        foregroundColor: AppColors.textPrimary,
        title: const Text('Answer SlamBook'),
      ),
      body: detailAsync.when(
        data: (detail) {
          final slambook = detail.slambook;
          final viewerSession = detail.viewerSession;
          _hydrateFromDetail(detail);
          final hydrationKey = _detailHydrationKey(detail);
          final readyToRender = _hydratedDetailKey == hydrationKey &&
              detail.cards.every((card) => _controllers.containsKey(card.id));

          if (!readyToRender) {
            return const Center(
              child: CircularProgressIndicator(color: AppColors.postbookPrimary),
            );
          }

          final editable = viewerSession == null || viewerSession.status == 'draft';
          final anonymousAllowed = slambook.responseIdentityMode != 'named';

          return ListView(
            padding: AppSpacing.pagePadding.copyWith(top: 8, bottom: 32),
            children: [
              Container(
                padding: const EdgeInsets.all(16),
                decoration: BoxDecoration(
                  color: AppColors.bgCard,
                  borderRadius: BorderRadius.circular(AppSpacing.radiusXL),
                  border: Border.all(color: AppColors.borderSubtle),
                ),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(slambook.title, style: AppTextStyles.h2),
                    if ((slambook.subtitle ?? '').trim().isNotEmpty) ...[
                      const SizedBox(height: 4),
                      Text(slambook.subtitle!, style: AppTextStyles.bodySmall),
                    ],
                    const SizedBox(height: 10),
                    TextField(
                      controller: _displayNameController,
                      enabled: editable,
                      decoration: const InputDecoration(
                        labelText: 'Display name',
                        hintText: 'How should this appear if not anonymous?',
                      ),
                    ),
                    if (anonymousAllowed)
                      SwitchListTile.adaptive(
                        value: _anonymous,
                        contentPadding: EdgeInsets.zero,
                        title: const Text('Post anonymously'),
                        subtitle: Text(
                          'Mode: ${slambookIdentityLabel(slambook.responseIdentityMode)}',
                        ),
                        onChanged: editable
                            ? (value) => setState(() => _anonymous = value)
                            : null,
                      ),
                    if (!editable) ...[
                      const SizedBox(height: 10),
                      Container(
                        padding: const EdgeInsets.all(12),
                        decoration: BoxDecoration(
                          color: AppColors.bgSecondary,
                          borderRadius: BorderRadius.circular(12),
                        ),
                        child: Text(
                          'This response has already been submitted with status: ${viewerSession.status}.',
                          style: AppTextStyles.bodySmall,
                        ),
                      ),
                    ],
                  ],
                ),
              ),
              const SizedBox(height: 18),
              ...detail.cards.map((card) {
                final controller = _controllerFor(card.id);
                return Padding(
                  padding: const EdgeInsets.only(bottom: 12),
                  child: Container(
                    padding: const EdgeInsets.all(14),
                    decoration: BoxDecoration(
                      color: AppColors.bgCard,
                      borderRadius: BorderRadius.circular(AppSpacing.radiusLarge),
                      border: Border.all(color: AppColors.borderSubtle),
                    ),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Row(
                          children: [
                            Expanded(child: Text(card.title, style: AppTextStyles.h3)),
                            if (card.isRequired)
                              Text(
                                'Required',
                                style: AppTextStyles.labelSmall.copyWith(
                                  color: AppColors.postbookPrimary,
                                ),
                              ),
                          ],
                        ),
                        const SizedBox(height: 6),
                        Text(card.prompt, style: AppTextStyles.bodySmall),
                        const SizedBox(height: 10),
                        TextField(
                          controller: controller,
                          enabled: editable,
                          minLines: card.responseType == 'long_text' ? 4 : 1,
                          maxLines: card.responseType == 'long_text' ? 5 : 2,
                          decoration: InputDecoration(
                            hintText: card.placeholderText ?? 'Write your answer',
                          ),
                        ),
                      ],
                    ),
                  ),
                );
              }),
              if (editable)
                Row(
                  children: [
                    Expanded(
                      child: OutlinedButton(
                        onPressed: _saving ? null : () => _save(detail, submit: false),
                        child: const Text('Save draft'),
                      ),
                    ),
                    const SizedBox(width: 10),
                    Expanded(
                      child: ElevatedButton(
                        onPressed: _saving ? null : () => _save(detail, submit: true),
                        style: ElevatedButton.styleFrom(
                          backgroundColor: AppColors.postbookPrimary,
                          foregroundColor: Colors.white,
                        ),
                        child: _saving
                            ? const SizedBox(
                                width: 16,
                                height: 16,
                                child: CircularProgressIndicator(
                                  strokeWidth: 2,
                                  color: Colors.white,
                                ),
                              )
                            : const Text('Submit'),
                      ),
                    ),
                  ],
                ),
              const SizedBox(height: 14),
              Container(
                padding: const EdgeInsets.all(12),
                decoration: BoxDecoration(
                  color: AppColors.bgSecondary,
                  borderRadius: BorderRadius.circular(12),
                ),
                child: Text(
                  widget.shareToken == null
                      ? 'If the owner shared a tokenized invite, this form can also continue that response flow.'
                      : 'You are responding through a share-token invite. Submit normally and the backend will validate access.',
                  style: AppTextStyles.bodySmall,
                ),
              ),
            ],
          );
        },
        loading: () => const Center(
          child: CircularProgressIndicator(color: AppColors.postbookPrimary),
        ),
        error: (_, _) => const Center(
          child: Text('Could not load the response form.'),
        ),
      ),
    );
  }
}
