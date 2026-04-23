import 'package:atpost_app/app/atpost_app.dart';
import 'package:atpost_app/core/cache/cache_manager.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

void main() async {
  WidgetsFlutterBinding.ensureInitialized();

  // Non-blocking initialization or at least with a timeout to prevent app hang
  try {
    await CacheManager.init().timeout(const Duration(seconds: 10));
  } catch (e) {
    debugPrint('Initialization error: $e');
  }

  runApp(const ProviderScope(child: AtpostApp()));
}
