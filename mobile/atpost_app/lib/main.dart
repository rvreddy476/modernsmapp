import 'package:atpost_app/app/atpost_app.dart';
import 'package:atpost_app/core/cache/cache_manager.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

void main() async {
  WidgetsFlutterBinding.ensureInitialized();
  await CacheManager.init();
  runApp(const ProviderScope(child: AtpostApp()));
}
