import 'package:atpost_app/data/models/business_page.dart';
import 'package:atpost_app/data/repositories/pages_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Discovery list (approved pages), optionally filtered by search.
final pagesDiscoverProvider =
    FutureProvider.autoDispose.family<List<BusinessPage>, String>((ref, search) async {
  return ref.watch(pagesRepositoryProvider).discover(search: search);
});

/// Pages the current user owns/manages (any status).
final myPagesProvider = FutureProvider.autoDispose<List<BusinessPage>>((ref) async {
  return ref.watch(pagesRepositoryProvider).myPages();
});

/// Single page detail by handle or id (the enriched actions envelope).
final pageDetailProvider =
    FutureProvider.autoDispose.family<BusinessPage, String>((ref, handleOrId) async {
  return ref.watch(pagesRepositoryProvider).getPage(handleOrId);
});

/// Owner verification documents for a page.
final pageDocumentsProvider =
    FutureProvider.autoDispose.family<List<PageDocument>, String>((ref, pageId) async {
  return ref.watch(pagesRepositoryProvider).listDocuments(pageId);
});
