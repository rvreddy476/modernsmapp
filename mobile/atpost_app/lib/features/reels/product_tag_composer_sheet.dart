// In-video product-tag composer (mobile).
//
// Mirrors postbook-ui/src/features/posttube/components/
// ProductTagComposer.tsx. Modal bottom sheet — creator picks an
// affiliate link, places it (X%/Y% + optional time window), submits.
// Existing tags shown below the picker for quick removal.
//
// Open from a "Tag products" action button on the reel/watch screen
// when the viewer is the post's author:
//
//   final r = await showModalBottomSheet<bool>(
//     context: context,
//     isScrollControlled: true,
//     builder: (_) => ProductTagComposerSheet(postId: postId),
//   );
//
// Returns true when at least one tag was created/deleted so the
// caller can refresh the overlay state.

import 'package:atpost_app/data/models/affiliate_link.dart';
import 'package:atpost_app/data/models/product_tag.dart';
import 'package:atpost_app/data/repositories/affiliate_links_repository.dart';
import 'package:atpost_app/data/repositories/product_tags_repository.dart';
import 'package:atpost_app/providers/product_tags_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class ProductTagComposerSheet extends ConsumerStatefulWidget {
  const ProductTagComposerSheet({super.key, required this.postId});

  final String postId;

  @override
  ConsumerState<ProductTagComposerSheet> createState() =>
      _ProductTagComposerSheetState();
}

class _ProductTagComposerSheetState
    extends ConsumerState<ProductTagComposerSheet> {
  AffiliateLink? _pickedLink;
  double _posX = 50;
  double _posY = 80;
  int? _timeStartMs;
  int? _timeEndMs;

  bool _submitting = false;
  String? _error;
  bool _anyMutation = false;

  Future<void> _create() async {
    final picked = _pickedLink;
    if (picked == null) return;

    setState(() {
      _submitting = true;
      _error = null;
    });

    try {
      // Best-effort enrich the tag with product label + image_url.
      // Cached on the tag row so the player avoids a fetch on every
      // viewer; refreshed by the product-update event consumer
      // server-side.
      final preview =
          await ref.read(affiliateLinksRepositoryProvider).getProductPreview(picked.listingId);

      await ref.read(productTagsRepositoryProvider).create(
            postId: widget.postId,
            affiliateLinkId: picked.id,
            positionX: _posX,
            positionY: _posY,
            timeStartMs: _timeStartMs,
            timeEndMs: _timeEndMs,
            label: preview?.title,
            // primary_image_media_id from preview would need
            // media-service resolution to a URL; the composer can
            // leave imageUrl empty and let the server-side
            // enrichment job fill it (TODO). Field is nullable.
          );

      _anyMutation = true;
      // Drop the cached list so the watch screen's overlay re-fetches.
      ref.invalidate(productTagsByPostProvider(widget.postId));

      if (!mounted) return;
      setState(() {
        _pickedLink = null;
        _timeStartMs = null;
        _timeEndMs = null;
        _submitting = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _submitting = false;
        _error = e.toString();
      });
    }
  }

  Future<void> _delete(PostProductTag tag) async {
    try {
      await ref.read(productTagsRepositoryProvider).delete(
            postId: widget.postId,
            tagId: tag.id,
          );
      _anyMutation = true;
      ref.invalidate(productTagsByPostProvider(widget.postId));
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Failed to remove tag: $e')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final linksAsync = ref.watch(myAffiliateLinksProvider);
    final tagsAsync = ref.watch(productTagsByPostProvider(widget.postId));

    final inset = MediaQuery.of(context).viewInsets.bottom;

    return SafeArea(
      child: Padding(
        padding: EdgeInsets.only(bottom: inset),
        child: SizedBox(
          height: MediaQuery.of(context).size.height * 0.8,
          child: Column(
            children: [
              _SheetHandle(),
              _SheetHeader(onClose: () => Navigator.of(context).pop(_anyMutation)),
              Expanded(
                child: ListView(
                  padding: const EdgeInsets.symmetric(horizontal: 16),
                  children: [
                    const _SectionLabel('Your affiliate links'),
                    linksAsync.when(
                      loading: () => const _LoadingRow(),
                      error: (e, _) => _ErrorRow('Failed to load links: $e'),
                      data: (links) => Column(
                        children: [
                          if (links.isEmpty)
                            const _Hint(
                              'No affiliate links yet. Create one in '
                              'Monetization → Affiliate first.',
                            )
                          else
                            for (final link in links)
                              _LinkRow(
                                link: link,
                                picked: _pickedLink?.id == link.id,
                                onPick: () =>
                                    setState(() => _pickedLink = link),
                              ),
                        ],
                      ),
                    ),
                    if (_pickedLink != null) ...[
                      const SizedBox(height: 8),
                      _PlacementForm(
                        posX: _posX,
                        posY: _posY,
                        timeStartMs: _timeStartMs,
                        timeEndMs: _timeEndMs,
                        onPosXChanged: (v) => setState(() => _posX = v),
                        onPosYChanged: (v) => setState(() => _posY = v),
                        onTimeStartChanged: (v) =>
                            setState(() => _timeStartMs = v),
                        onTimeEndChanged: (v) =>
                            setState(() => _timeEndMs = v),
                        submitting: _submitting,
                        error: _error,
                        onCancel: () =>
                            setState(() => _pickedLink = null),
                        onSubmit: _create,
                      ),
                    ],
                    const SizedBox(height: 16),
                    const _SectionLabel('Tags on this video'),
                    tagsAsync.when(
                      loading: () => const _LoadingRow(),
                      error: (e, _) => _ErrorRow('Failed to load tags: $e'),
                      data: (tags) => Column(
                        children: [
                          if (tags.isEmpty)
                            const _Hint('No tags placed yet.')
                          else
                            for (final tag in tags)
                              _TagRow(
                                tag: tag,
                                onDelete: () => _delete(tag),
                              ),
                        ],
                      ),
                    ),
                    const SizedBox(height: 24),
                  ],
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

// ─── header + section labels ────────────────────────────────────────

class _SheetHandle extends StatelessWidget {
  @override
  Widget build(BuildContext context) {
    return Container(
      width: 36,
      height: 4,
      margin: const EdgeInsets.only(top: 8),
      decoration: BoxDecoration(
        color: Colors.black.withValues(alpha: 0.16),
        borderRadius: BorderRadius.circular(2),
      ),
    );
  }
}

class _SheetHeader extends StatelessWidget {
  const _SheetHeader({required this.onClose});

  final VoidCallback onClose;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 12, 8, 12),
      child: Row(
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: const [
                Text(
                  'Tag products in this video',
                  style: TextStyle(
                    fontSize: 16,
                    fontWeight: FontWeight.w600,
                    color: Colors.black87,
                  ),
                ),
                SizedBox(height: 2),
                Text(
                  'Viewers can tap your tag to buy — you earn the commission.',
                  style: TextStyle(fontSize: 12, color: Colors.black54),
                ),
              ],
            ),
          ),
          IconButton(
            icon: const Icon(Icons.close, size: 20),
            onPressed: onClose,
            tooltip: 'Close',
          ),
        ],
      ),
    );
  }
}

class _SectionLabel extends StatelessWidget {
  const _SectionLabel(this.text);
  final String text;
  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 8),
      child: Text(
        text.toUpperCase(),
        style: const TextStyle(
          fontSize: 10,
          fontWeight: FontWeight.w600,
          letterSpacing: 1.2,
          color: Colors.black54,
        ),
      ),
    );
  }
}

class _Hint extends StatelessWidget {
  const _Hint(this.text);
  final String text;
  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 12),
      child: Text(
        text,
        style: const TextStyle(fontSize: 13, color: Colors.black54),
      ),
    );
  }
}

class _LoadingRow extends StatelessWidget {
  const _LoadingRow();
  @override
  Widget build(BuildContext context) => const Padding(
        padding: EdgeInsets.symmetric(vertical: 12),
        child: SizedBox(height: 18, child: LinearProgressIndicator()),
      );
}

class _ErrorRow extends StatelessWidget {
  const _ErrorRow(this.text);
  final String text;
  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 12),
      child: Text(
        text,
        style: const TextStyle(fontSize: 13, color: Color(0xFFBE123C)),
      ),
    );
  }
}

// ─── link + tag rows ────────────────────────────────────────────────

class _LinkRow extends ConsumerWidget {
  const _LinkRow({
    required this.link,
    required this.picked,
    required this.onPick,
  });

  final AffiliateLink link;
  final bool picked;
  final VoidCallback onPick;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final previewAsync = ref.watch(productPreviewProvider(link.listingId));
    final title = previewAsync.maybeWhen(
      data: (p) => p?.title ?? 'Loading product…',
      orElse: () => 'Loading product…',
    );

    return Material(
      color: picked ? const Color(0xFFEDE9FE) : Colors.transparent,
      borderRadius: BorderRadius.circular(8),
      child: InkWell(
        borderRadius: BorderRadius.circular(8),
        onTap: onPick,
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 10),
          child: Row(
            children: [
              Container(
                width: 40,
                height: 40,
                decoration: BoxDecoration(
                  color: const Color(0xFFF1F5F9),
                  borderRadius: BorderRadius.circular(8),
                ),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      title,
                      style: const TextStyle(
                        fontSize: 14,
                        fontWeight: FontWeight.w500,
                        color: Colors.black87,
                      ),
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                    ),
                    const SizedBox(height: 2),
                    Text(
                      '${link.commissionPct.toStringAsFixed(0)}% · ${link.clickCount} clicks',
                      style: const TextStyle(fontSize: 11, color: Colors.black54),
                    ),
                  ],
                ),
              ),
              if (picked)
                const Icon(Icons.check_circle, color: Color(0xFF7C3AED), size: 20),
            ],
          ),
        ),
      ),
    );
  }
}

class _TagRow extends StatelessWidget {
  const _TagRow({required this.tag, required this.onDelete});

  final PostProductTag tag;
  final VoidCallback onDelete;

  @override
  Widget build(BuildContext context) {
    final window = (tag.timeStartMs == null && tag.timeEndMs == null)
        ? 'Whole video'
        : '${_fmt(tag.timeStartMs)} → ${_fmt(tag.timeEndMs)}';
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 6),
      child: Row(
        children: [
          Container(
            width: 40,
            height: 40,
            decoration: BoxDecoration(
              color: const Color(0xFFF1F5F9),
              borderRadius: BorderRadius.circular(8),
            ),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  tag.label.isEmpty ? 'Untitled tag' : tag.label,
                  style: const TextStyle(
                    fontSize: 14,
                    fontWeight: FontWeight.w500,
                    color: Colors.black87,
                  ),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
                const SizedBox(height: 2),
                Text(
                  '$window · ${tag.impressionCount} views · ${tag.clickCount} taps',
                  style: const TextStyle(fontSize: 11, color: Colors.black54),
                ),
              ],
            ),
          ),
          TextButton(
            onPressed: onDelete,
            style: TextButton.styleFrom(
              foregroundColor: const Color(0xFFBE123C),
              minimumSize: const Size(48, 32),
            ),
            child: const Text('Remove'),
          ),
        ],
      ),
    );
  }
}

String _fmt(int? ms) {
  if (ms == null) return '—';
  final totalSec = ms ~/ 1000;
  final m = totalSec ~/ 60;
  final s = totalSec % 60;
  return '$m:${s.toString().padLeft(2, '0')}';
}

// ─── placement form ────────────────────────────────────────────────

class _PlacementForm extends StatelessWidget {
  const _PlacementForm({
    required this.posX,
    required this.posY,
    required this.timeStartMs,
    required this.timeEndMs,
    required this.onPosXChanged,
    required this.onPosYChanged,
    required this.onTimeStartChanged,
    required this.onTimeEndChanged,
    required this.submitting,
    required this.error,
    required this.onCancel,
    required this.onSubmit,
  });

  final double posX;
  final double posY;
  final int? timeStartMs;
  final int? timeEndMs;
  final ValueChanged<double> onPosXChanged;
  final ValueChanged<double> onPosYChanged;
  final ValueChanged<int?> onTimeStartChanged;
  final ValueChanged<int?> onTimeEndChanged;
  final bool submitting;
  final String? error;
  final VoidCallback onCancel;
  final VoidCallback onSubmit;

  @override
  Widget build(BuildContext context) {
    return Container(
      margin: const EdgeInsets.only(top: 8),
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: const Color(0xFFF8FAFC),
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: const Color(0xFFE2E8F0)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Text(
            'PLACEMENT',
            style: TextStyle(
              fontSize: 10,
              fontWeight: FontWeight.w600,
              letterSpacing: 1.2,
              color: Colors.black54,
            ),
          ),
          const SizedBox(height: 8),
          _SliderRow(
            label: 'X position',
            value: posX,
            onChanged: onPosXChanged,
          ),
          _SliderRow(
            label: 'Y position',
            value: posY,
            onChanged: onPosYChanged,
          ),
          const SizedBox(height: 8),
          Row(
            children: [
              Expanded(
                child: _TimeField(
                  label: 'Start (ms)',
                  value: timeStartMs,
                  onChanged: onTimeStartChanged,
                ),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: _TimeField(
                  label: 'End (ms)',
                  value: timeEndMs,
                  onChanged: onTimeEndChanged,
                ),
              ),
            ],
          ),
          if (error != null) ...[
            const SizedBox(height: 8),
            Text(
              error!,
              style: const TextStyle(fontSize: 12, color: Color(0xFFBE123C)),
            ),
          ],
          const SizedBox(height: 12),
          Row(
            mainAxisAlignment: MainAxisAlignment.end,
            children: [
              TextButton(
                onPressed: submitting ? null : onCancel,
                child: const Text('Cancel'),
              ),
              const SizedBox(width: 8),
              FilledButton(
                onPressed: submitting ? null : onSubmit,
                style: FilledButton.styleFrom(
                  backgroundColor: const Color(0xFF7C3AED),
                ),
                child: Text(submitting ? 'Adding…' : 'Add tag'),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

class _SliderRow extends StatelessWidget {
  const _SliderRow({
    required this.label,
    required this.value,
    required this.onChanged,
  });

  final String label;
  final double value;
  final ValueChanged<double> onChanged;

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        SizedBox(
          width: 72,
          child: Text(
            label,
            style: const TextStyle(fontSize: 12, color: Colors.black87),
          ),
        ),
        Expanded(
          child: Slider(
            value: value,
            min: 0,
            max: 100,
            divisions: 100,
            label: '${value.toStringAsFixed(0)}%',
            activeColor: const Color(0xFF7C3AED),
            onChanged: onChanged,
          ),
        ),
        SizedBox(
          width: 40,
          child: Text(
            '${value.toStringAsFixed(0)}%',
            textAlign: TextAlign.right,
            style: const TextStyle(fontSize: 12, color: Colors.black54),
          ),
        ),
      ],
    );
  }
}

class _TimeField extends StatelessWidget {
  const _TimeField({
    required this.label,
    required this.value,
    required this.onChanged,
  });

  final String label;
  final int? value;
  final ValueChanged<int?> onChanged;

  @override
  Widget build(BuildContext context) {
    return TextField(
      keyboardType: TextInputType.number,
      controller: TextEditingController(text: value?.toString() ?? ''),
      decoration: InputDecoration(
        labelText: label,
        isDense: true,
        border: const OutlineInputBorder(),
      ),
      onChanged: (s) {
        final parsed = int.tryParse(s.trim());
        onChanged(parsed);
      },
    );
  }
}
