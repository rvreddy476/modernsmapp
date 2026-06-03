import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/page_types.dart';
import 'package:atpost_app/data/repositories/pages_repository.dart';
import 'package:atpost_app/providers/pages_provider.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

class CreatePageScreen extends ConsumerStatefulWidget {
  const CreatePageScreen({super.key});

  @override
  ConsumerState<CreatePageScreen> createState() => _CreatePageScreenState();
}

class _CreatePageScreenState extends ConsumerState<CreatePageScreen> {
  final _handle = TextEditingController();
  final _name = TextEditingController();
  final _category = TextEditingController();
  final _description = TextEditingController();
  String _pageType = '';
  String _error = '';
  bool _busy = false;

  @override
  void dispose() {
    _handle.dispose();
    _name.dispose();
    _category.dispose();
    _description.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    setState(() => _error = '');
    if (_handle.text.trim().isEmpty || _name.text.trim().isEmpty || _pageType.isEmpty) {
      setState(() => _error = 'Handle, name, and page type are required.');
      return;
    }
    setState(() => _busy = true);
    try {
      final page = await ref.read(pagesRepositoryProvider).createPage(
            pageHandle: _handle.text.trim().toLowerCase().replaceAll(RegExp(r'\s+'), '-'),
            pageName: _name.text.trim(),
            pageType: _pageType,
            category: _category.text.trim().isEmpty ? null : _category.text.trim(),
            description: _description.text.trim().isEmpty ? null : _description.text.trim(),
          );
      ref.invalidate(myPagesProvider);
      if (mounted) context.go('/page/${page.pageHandle}');
    } catch (e) {
      final s = e.toString();
      setState(() => _error = s.contains('HANDLE_TAKEN')
          ? 'This handle is already taken.'
          : 'Could not create the page. Try again.');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final selected = pageTypeByValue(_pageType);
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Create Page', style: AppTextStyles.h2),
      ),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          if (_error.isNotEmpty)
            Container(
              margin: const EdgeInsets.only(bottom: 12),
              padding: const EdgeInsets.all(12),
              decoration: BoxDecoration(color: const Color(0xFF3A1620), borderRadius: BorderRadius.circular(10)),
              child: Text(_error, style: AppTextStyles.labelSmall.copyWith(color: AppColors.statusError)),
            ),
          _field('Page handle *', _handle, hint: 'your-page'),
          _field('Page name *', _name, hint: 'Anika Foods'),
          const SizedBox(height: 12),
          Text('Page type *', style: AppTextStyles.label),
          const SizedBox(height: 6),
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 12),
            decoration: BoxDecoration(color: AppColors.bgSecondary, borderRadius: BorderRadius.circular(12)),
            child: DropdownButtonHideUnderline(
              child: DropdownButton<String>(
                value: _pageType.isEmpty ? null : _pageType,
                isExpanded: true,
                dropdownColor: AppColors.bgSecondary,
                hint: Text('Select a page type', style: AppTextStyles.body.copyWith(color: AppColors.textTertiary)),
                style: AppTextStyles.body,
                items: kPageTypes
                    .map((t) => DropdownMenuItem(value: t.value, child: Text(t.label)))
                    .toList(),
                onChanged: (v) => setState(() => _pageType = v ?? ''),
              ),
            ),
          ),
          if (selected != null)
            Padding(
              padding: const EdgeInsets.only(top: 6),
              child: Text(
                selected.requiredDocuments.isEmpty
                    ? selected.description
                    : '${selected.description} Requires: ${selected.requiredDocuments.map(documentLabel).join(", ")}.',
                style: AppTextStyles.labelSmall.copyWith(color: AppColors.textTertiary),
              ),
            ),
          const SizedBox(height: 12),
          _field('Category (optional)', _category, hint: 'e.g. Italian restaurant'),
          _field('Description', _description, hint: 'Tell people about this page…', maxLines: 3),
          const SizedBox(height: 20),
          GestureDetector(
            onTap: _busy ? null : _submit,
            child: Container(
              height: 48,
              alignment: Alignment.center,
              decoration: BoxDecoration(color: AppColors.postbookPrimary, borderRadius: BorderRadius.circular(14)),
              child: _busy
                  ? const SizedBox(width: 20, height: 20, child: CircularProgressIndicator(strokeWidth: 2, color: Colors.white))
                  : Text('Create Page', style: AppTextStyles.label.copyWith(color: Colors.white)),
            ),
          ),
          const SizedBox(height: 8),
          Text(
            'New pages start as a draft. Upload the required documents and submit for review to go live.',
            style: AppTextStyles.labelSmall.copyWith(color: AppColors.textTertiary),
          ),
        ],
      ),
    );
  }

  Widget _field(String label, TextEditingController c, {String? hint, int maxLines = 1}) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(label, style: AppTextStyles.label),
          const SizedBox(height: 6),
          TextField(
            controller: c,
            maxLines: maxLines,
            style: AppTextStyles.body,
            decoration: InputDecoration(
              hintText: hint,
              hintStyle: AppTextStyles.body.copyWith(color: AppColors.textTertiary),
              filled: true,
              fillColor: AppColors.bgSecondary,
              border: OutlineInputBorder(borderRadius: BorderRadius.circular(12), borderSide: BorderSide.none),
            ),
          ),
        ],
      ),
    );
  }
}
