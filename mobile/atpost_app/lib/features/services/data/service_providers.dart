import 'package:atpost_app/core/cache/cache_manager.dart';
import 'package:atpost_app/features/services/data/service_permissions_store.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

final servicePermissionsStoreProvider =
    Provider<ServicePermissionsStore>((ref) {
  return ServicePermissionsStore(CacheManager());
});
