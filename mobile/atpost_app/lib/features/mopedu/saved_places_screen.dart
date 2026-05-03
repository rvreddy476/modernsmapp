// Mopedu — saved places editor.
//
// Lists existing saved places (Home / Work / School / Hospital + recents).
// Add/edit form takes a label, lat/lng (text inputs for v1) and kind.
// Telemetry: `mopedu.saved_place.added` is fired by the notifier inside
// `mopedu_providers.dart` — no extra event here.

import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/models/mopedu.dart';
import 'package:atpost_app/providers/mopedu_providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class SavedPlacesScreen extends ConsumerWidget {
  const SavedPlacesScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final asyncPlaces = ref.watch(savedPlacesProvider);

    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Saved places', style: AppTextStyles.h2),
        leading: IconButton(
          icon: const Icon(
            Icons.arrow_back_ios_new_rounded,
            color: AppColors.textPrimary,
            size: 18,
          ),
          onPressed: () =>
              context.canPop() ? context.pop() : context.go('/mopedu'),
        ),
      ),
      body: asyncPlaces.when(
        data: (list) => _Body(places: list),
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => Center(
          child: Padding(
            padding: const EdgeInsets.all(24),
            child: Text(
              'Could not load saved places.\n$e',
              style: AppTextStyles.bodySmall,
              textAlign: TextAlign.center,
            ),
          ),
        ),
      ),
      floatingActionButton: FloatingActionButton.extended(
        backgroundColor: AppColors.postbookPrimary,
        onPressed: () => _editPlace(context, ref, null),
        icon: const Icon(Icons.add),
        label: const Text('Add place'),
      ),
    );
  }

  Future<void> _editPlace(
    BuildContext context,
    WidgetRef ref,
    SavedPlace? existing,
  ) async {
    final result = await showModalBottomSheet<SavedPlace?>(
      context: context,
      isScrollControlled: true,
      backgroundColor: AppColors.bgSecondary,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      builder: (_) => _PlaceEditor(existing: existing),
    );
    if (result != null) {
      await ref.read(savedPlacesProvider.notifier).add(result);
    }
  }
}

class _Body extends ConsumerWidget {
  const _Body({required this.places});

  final List<SavedPlace> places;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final byKind = <String, SavedPlace?>{
      SavedPlaceKind.home: null,
      SavedPlaceKind.work: null,
      SavedPlaceKind.school: null,
      SavedPlaceKind.hospital: null,
    };
    for (final p in places) {
      if (byKind.containsKey(p.kind)) byKind[p.kind] = p;
    }
    final recents = places
        .where((p) => p.kind == SavedPlaceKind.recent)
        .toList()
        .reversed
        .toList();

    return ListView(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 100),
      children: [
        Text('Quick places', style: AppTextStyles.h3),
        const SizedBox(height: 8),
        for (final kind in SavedPlaceKind.fixed)
          _PlaceTile(
            kind: kind,
            place: byKind[kind],
            onEdit: () => _SavedPlacesScreenState._open(context, ref, byKind[kind], kind),
            onDelete: byKind[kind] == null
                ? null
                : () =>
                    ref.read(savedPlacesProvider.notifier).remove(byKind[kind]!.id),
          ),
        const SizedBox(height: 16),
        if (recents.isNotEmpty) ...[
          Text('Recents', style: AppTextStyles.h3),
          const SizedBox(height: 8),
          for (final r in recents)
            _PlaceTile(
              kind: r.kind,
              place: r,
              onEdit: () => _SavedPlacesScreenState._open(context, ref, r, r.kind),
              onDelete: () =>
                  ref.read(savedPlacesProvider.notifier).remove(r.id),
            ),
        ],
      ],
    );
  }
}

/// Static helper that re-uses the screen-level editor opener.
class _SavedPlacesScreenState {
  static Future<void> _open(
    BuildContext context,
    WidgetRef ref,
    SavedPlace? existing,
    String defaultKind,
  ) async {
    final result = await showModalBottomSheet<SavedPlace?>(
      context: context,
      isScrollControlled: true,
      backgroundColor: AppColors.bgSecondary,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      builder: (_) => _PlaceEditor(
        existing: existing,
        defaultKind: defaultKind,
      ),
    );
    if (result != null) {
      await ref.read(savedPlacesProvider.notifier).add(result);
    }
  }
}

class _PlaceTile extends StatelessWidget {
  const _PlaceTile({
    required this.kind,
    required this.place,
    required this.onEdit,
    this.onDelete,
  });

  final String kind;
  final SavedPlace? place;
  final VoidCallback onEdit;
  final VoidCallback? onDelete;

  IconData get _icon {
    switch (kind) {
      case SavedPlaceKind.home:
        return Icons.home;
      case SavedPlaceKind.work:
        return Icons.work;
      case SavedPlaceKind.school:
        return Icons.school;
      case SavedPlaceKind.hospital:
        return Icons.local_hospital;
      default:
        return Icons.place;
    }
  }

  @override
  Widget build(BuildContext context) {
    final has = place != null;
    return Container(
      margin: const EdgeInsets.only(bottom: 8),
      decoration: BoxDecoration(
        color: AppColors.bgSecondary,
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        border: Border.all(color: AppColors.borderSubtle),
      ),
      child: ListTile(
        leading: Icon(
          _icon,
          color: has ? AppColors.postbookPrimary : AppColors.textTertiary,
        ),
        title: Text(
          has ? place!.label : SavedPlaceKind.label(kind),
          style: AppTextStyles.label,
        ),
        subtitle: has
            ? Text(
                place!.point.displayName,
                style: AppTextStyles.bodySmall,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              )
            : Text('Not set yet', style: AppTextStyles.bodySmall),
        trailing: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            IconButton(
              icon: Icon(
                has ? Icons.edit : Icons.add,
                color: AppColors.textTertiary,
                size: 18,
              ),
              onPressed: onEdit,
            ),
            if (onDelete != null)
              IconButton(
                icon: const Icon(
                  Icons.delete_outline,
                  color: AppColors.statusError,
                  size: 18,
                ),
                onPressed: onDelete,
              ),
          ],
        ),
      ),
    );
  }
}

// ─── Editor sheet ─────────────────────────────────────────────────────

class _PlaceEditor extends StatefulWidget {
  const _PlaceEditor({this.existing, this.defaultKind});

  final SavedPlace? existing;
  final String? defaultKind;

  @override
  State<_PlaceEditor> createState() => _PlaceEditorState();
}

class _PlaceEditorState extends State<_PlaceEditor> {
  late final TextEditingController _label;
  late final TextEditingController _addr;
  late final TextEditingController _lat;
  late final TextEditingController _lng;
  late String _kind;

  @override
  void initState() {
    super.initState();
    final ex = widget.existing;
    _label = TextEditingController(text: ex?.label ?? '');
    _addr = TextEditingController(text: ex?.point.address ?? '');
    _lat = TextEditingController(
      text: ex == null ? '' : ex.point.lat.toString(),
    );
    _lng = TextEditingController(
      text: ex == null ? '' : ex.point.lng.toString(),
    );
    _kind = ex?.kind ?? widget.defaultKind ?? SavedPlaceKind.home;
  }

  @override
  void dispose() {
    _label.dispose();
    _addr.dispose();
    _lat.dispose();
    _lng.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final pad = MediaQuery.of(context).viewInsets.bottom;
    return Padding(
      padding: EdgeInsets.fromLTRB(16, 16, 16, 16 + pad),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text(
            widget.existing == null ? 'Add place' : 'Edit place',
            style: AppTextStyles.h2,
          ),
          const SizedBox(height: 12),
          _KindPicker(
            value: _kind,
            onChanged: (v) => setState(() => _kind = v),
          ),
          const SizedBox(height: 8),
          _Field(controller: _label, hint: 'Label (e.g. Home, Office)'),
          const SizedBox(height: 8),
          _Field(controller: _addr, hint: 'Address'),
          const SizedBox(height: 8),
          Row(
            children: [
              Expanded(
                child: _Field(
                  controller: _lat,
                  hint: 'Latitude',
                  keyboard: TextInputType.number,
                ),
              ),
              const SizedBox(width: 8),
              Expanded(
                child: _Field(
                  controller: _lng,
                  hint: 'Longitude',
                  keyboard: TextInputType.number,
                ),
              ),
            ],
          ),
          const SizedBox(height: 12),
          Row(
            children: [
              Expanded(
                child: OutlinedButton(
                  onPressed: () => Navigator.of(context).pop(),
                  child: const Text('Cancel'),
                ),
              ),
              const SizedBox(width: 8),
              Expanded(
                child: ElevatedButton(
                  style: ElevatedButton.styleFrom(
                    backgroundColor: AppColors.postbookPrimary,
                    foregroundColor: Colors.white,
                  ),
                  onPressed: _onSave,
                  child: const Text('Save'),
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }

  void _onSave() {
    final label = _label.text.trim();
    if (label.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Enter a label')),
      );
      return;
    }
    final lat = double.tryParse(_lat.text.trim()) ?? 0;
    final lng = double.tryParse(_lng.text.trim()) ?? 0;
    if (lat == 0 && lng == 0) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Enter a valid latitude and longitude')),
      );
      return;
    }
    final id = widget.existing?.id ??
        '${_kind}_${DateTime.now().millisecondsSinceEpoch}';
    Navigator.of(context).pop(
      SavedPlace(
        id: id,
        kind: _kind,
        label: label,
        point: RidePoint(
          lat: lat,
          lng: lng,
          address: _addr.text.trim().isEmpty ? null : _addr.text.trim(),
          placeName: label,
        ),
      ),
    );
  }
}

class _KindPicker extends StatelessWidget {
  const _KindPicker({required this.value, required this.onChanged});

  final String value;
  final ValueChanged<String> onChanged;

  @override
  Widget build(BuildContext context) {
    return Wrap(
      spacing: 8,
      runSpacing: 8,
      children: [
        for (final k in SavedPlaceKind.fixed)
          ChoiceChip(
            label: Text(SavedPlaceKind.label(k)),
            selected: value == k,
            onSelected: (_) => onChanged(k),
            backgroundColor: AppColors.bgTertiary,
            selectedColor: AppColors.postbookPrimary.withValues(alpha: 0.2),
            labelStyle: AppTextStyles.labelSmall,
          ),
      ],
    );
  }
}

class _Field extends StatelessWidget {
  const _Field({
    required this.controller,
    required this.hint,
    this.keyboard,
  });

  final TextEditingController controller;
  final String hint;
  final TextInputType? keyboard;

  @override
  Widget build(BuildContext context) {
    return TextField(
      controller: controller,
      keyboardType: keyboard,
      style: AppTextStyles.body,
      decoration: InputDecoration(
        hintText: hint,
        hintStyle: AppTextStyles.bodySmall,
        filled: true,
        fillColor: AppColors.bgTertiary,
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
          borderSide: BorderSide.none,
        ),
        contentPadding: const EdgeInsets.symmetric(
          horizontal: 12,
          vertical: 12,
        ),
      ),
    );
  }
}
