import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/data/models/slambook.dart';
import 'package:atpost_app/data/repositories/memories_repository.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

final slambookTemplatePacksProvider =
    FutureProvider.autoDispose<List<SlambookTemplatePack>>((ref) async {
      return ref.watch(memoriesRepositoryProvider).getSlambookTemplatePacks();
    });

final mySlambooksProvider =
    FutureProvider.autoDispose<List<Slambook>>((ref) async {
      final userId = ref.watch(authServiceProvider).userId;
      if (userId == null || userId.isEmpty) {
        return const <Slambook>[];
      }
      return ref.watch(memoriesRepositoryProvider).getSlambooks(ownerUserId: userId);
    });

final slambookDetailProvider =
    FutureProvider.autoDispose.family<SlambookDetail, String>((ref, slambookId) async {
      return ref.watch(memoriesRepositoryProvider).getSlambook(slambookId);
    });

final slambookShareDetailProvider =
    FutureProvider.autoDispose.family<SlambookDetail, String>((ref, shareToken) async {
      return ref.watch(memoriesRepositoryProvider).getSlambookByShareToken(shareToken);
    });

final slambookOpinionSpaceProvider = FutureProvider.autoDispose
    .family<List<SlambookOpinionSpaceItem>, String>((ref, slambookId) async {
      return ref.watch(memoriesRepositoryProvider).getSlambookOpinionSpace(slambookId);
    });

final slambookModerationQueueProvider = FutureProvider.autoDispose
    .family<List<SlambookResponseSession>, String>((ref, slambookId) async {
      return ref.watch(memoriesRepositoryProvider).getSlambookModerationQueue(slambookId);
    });

Color slambookAccentColor(String themeKey) {
  switch (themeKey) {
    case 'sunset':
    case 'warm':
      return const Color(0xFFFF6B35);
    case 'mint':
    case 'ocean':
      return const Color(0xFF4ECDC4);
    case 'violet':
    case 'retro':
      return const Color(0xFF7B68EE);
    default:
      return AppColors.postbookPrimary;
  }
}

String slambookVisibilityLabel(String visibility) {
  return visibility.replaceAll('_', ' ');
}

String slambookIdentityLabel(String identityMode) {
  return identityMode.replaceAll('_', ' ');
}

String slambookRelativeDate(DateTime timestamp) {
  final now = DateTime.now();
  final diff = now.difference(timestamp);
  if (diff.inMinutes < 1) {
    return 'just now';
  }
  if (diff.inHours < 1) {
    return '${diff.inMinutes}m ago';
  }
  if (diff.inDays < 1) {
    return '${diff.inHours}h ago';
  }
  if (diff.inDays < 7) {
    return '${diff.inDays}d ago';
  }
  return '${timestamp.day}/${timestamp.month}/${timestamp.year}';
}

String slambookAnswerPreview(SlambookResponseItem item) {
  final text = item.answerText?.trim();
  if (text != null && text.isNotEmpty) {
    return text;
  }
  if (item.answerJson.isEmpty) {
    return 'No answer text';
  }
  return item.answerJson.values.map((value) => value.toString()).join(', ');
}

String slambookBoardPreview(SlambookOpinionSpaceItem item) {
  final text = item.answerText?.trim();
  if (text != null && text.isNotEmpty) {
    return text;
  }
  if (item.answerJson.isEmpty) {
    return 'No answer text';
  }
  return item.answerJson.values.map((value) => value.toString()).join(', ');
}
