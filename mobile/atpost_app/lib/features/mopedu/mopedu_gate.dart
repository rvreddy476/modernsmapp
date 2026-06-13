import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/repositories/mopedu_repository.dart';
import 'package:atpost_app/features/mopedu/mopedu_waitlist_screen.dart';
import 'package:atpost_app/providers/mopedu_providers.dart';
import 'package:atpost_app/providers/user_provider.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:atpost_app/services/mopedu_crash_breadcrumbs.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Sprint 5 — master Mopedu rollout gate. Mirrors the Pulse Sprint 6
/// `pulse_gate.dart` template.
///
/// Three pieces glue together here:
///
///   1. `mopeduEnabledProvider` — resolves the master feature flag
///      `mopedu_enabled_master` from feature-flag-service. While the
///      flag is OFF, the entire Mopedu surface is hidden.
///
///   2. `mopeduCityAllowedProvider` — Bengaluru-only city gate. The v1
///      launch ships only to Bengaluru / Bangalore. Profiles in any
///      other city see the waitlist screen instead of the booking
///      surface.
///
///   3. `MopeduGate` widget — wraps a Mopedu route. Renders, in order:
///      loading shimmer → "rolling out city by city" empty state →
///      waitlist screen → child.
///
/// All three knobs are opaque to the rest of the codebase — the launch
/// runbook says to flip flags via feature-flag-service, never via code.

/// Hard-coded list of cities allowed for the v1 soft launch.
/// Case-insensitive. Adding a city requires a deploy because the launch
/// runbook calls out "every flag flip is a deploy event".
const List<String> kMopeduAllowedCitiesV1 = <String>[
  'Bengaluru',
  'Bangalore',
];

/// Master switch — `mopedu_enabled_master` in feature-flag-service.
/// Errors default to OFF (safer to leave Mopedu hidden than expose a
/// half-built experience). Cached as `FutureProvider` so once the flag
/// resolves the Mopedu surface does not flicker as the user navigates.
final mopeduEnabledProvider = FutureProvider<bool>((ref) async {
  final api = ref.watch(apiClientProvider);
  try {
    final response = await api.get(
      '/v1/flags/me',
      queryParameters: {'key': 'mopedu_enabled_master'},
    );
    final body = response.data;
    if (body is Map<String, dynamic>) {
      final raw = body['data'];
      final data =
          raw is Map<String, dynamic> ? raw : body;
      final enabled = data['enabled'];
      if (enabled is bool) return enabled;
    }
    return false;
  } catch (e, st) {
    MopeduBreadcrumbs.error(
      'mopedu_enabled flag fetch failed',
      error: e,
      stackTrace: st,
    );
    return false;
  }
});

/// Result of the city gate. Composed at one point so screens don't have
/// to re-implement the case-insensitive membership check.
class MopeduCityGateState {
  const MopeduCityGateState({
    required this.allowed,
    required this.userCity,
  });

  /// True when the user's city is in the v1 allowed list.
  final bool allowed;

  /// The user's city (empty string when unset).
  final String userCity;

  /// `null` when the user has no city; useful for "Set your city" prompts.
  String? get displayCity => userCity.isEmpty ? null : userCity;
}

/// Determines whether the current user's city is in the v1 allowed list.
///
/// Source order:
///   1. `currentUserProvider.profile.location` (the AtPost-wide profile
///      city).
///   2. The Mopedu-side selected city id resolved against the cities
///      list (`riderCitiesProvider`).
///
/// Errors fall through to `allowed=false` with an empty `userCity` so
/// the UX shows the friendly "Set your city" prompt instead of crashing.
final mopeduCityAllowedProvider =
    FutureProvider<MopeduCityGateState>((ref) async {
  String userCity = '';
  try {
    final user = await ref.watch(currentUserProvider.future);
    if (user.location != null && user.location!.isNotEmpty) {
      userCity = user.location!;
    }
  } catch (_) {
    // ignore — fall through to the Mopedu-side fallback
  }
  if (userCity.isEmpty) {
    try {
      final selectedId = ref.watch(selectedCityProvider);
      if (selectedId != null) {
        final cities = await ref.watch(riderCitiesProvider.future);
        final m =
            cities.where((c) => c.id == selectedId).toList(growable: false);
        if (m.isNotEmpty) userCity = m.first.name;
      }
    } catch (_) {
      // ignore
    }
  }
  final allowed = _cityMatches(userCity, kMopeduAllowedCitiesV1);
  return MopeduCityGateState(allowed: allowed, userCity: userCity);
});

bool _cityMatches(String city, List<String> allowed) {
  if (city.isEmpty) return false;
  final lower = city.trim().toLowerCase();
  for (final c in allowed) {
    if (lower == c.toLowerCase()) return true;
  }
  return false;
}

/// `MopeduGate` — wraps a Mopedu screen.
///
/// Renders, in order of priority:
///   1. Loading shimmer while the master flag resolves.
///   2. "Mopedu is rolling out city by city" empty state when the
///      master flag is OFF.
///   3. The city-gate waitlist screen when the master flag is ON but
///      the user is outside the allowed city list.
///   4. The wrapped child otherwise.
class MopeduGate extends ConsumerWidget {
  const MopeduGate({super.key, required this.child});

  final Widget child;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final flagAsync = ref.watch(mopeduEnabledProvider);

    return flagAsync.when(
      loading: () => const _MopeduGateLoading(),
      error: (_, _) => const _MopeduRolloutEmpty(),
      data: (enabled) {
        if (!enabled) {
          MopeduBreadcrumbs.gateBlocked(reason: 'master_flag_off');
          return const _MopeduRolloutEmpty();
        }
        final cityAsync = ref.watch(mopeduCityAllowedProvider);
        return cityAsync.when(
          loading: () => const _MopeduGateLoading(),
          error: (_, _) => child, // fail-open on city read error
          data: (state) {
            if (state.allowed) return child;
            MopeduBreadcrumbs.gateBlocked(
              reason: 'city_not_allowed',
              city: state.userCity.isEmpty ? null : state.userCity,
            );
            return MopeduWaitlistScreen(userCity: state.displayCity);
          },
        );
      },
    );
  }
}

class _MopeduGateLoading extends StatelessWidget {
  const _MopeduGateLoading();

  @override
  Widget build(BuildContext context) {
    return const Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: Center(
        child: CircularProgressIndicator(
          color: AppColors.posttubePrimary,
        ),
      ),
    );
  }
}

class _MopeduRolloutEmpty extends StatelessWidget {
  const _MopeduRolloutEmpty();

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Mopedu', style: AppTextStyles.h2),
      ),
      body: Padding(
        padding: AppSpacing.pagePadding,
        child: Center(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Icon(
                Icons.directions_car_outlined,
                size: 64,
                color: AppColors.posttubePrimary,
              ),
              const SizedBox(height: 24),
              Text(
                'Mopedu is rolling out city by city.',
                style: AppTextStyles.h2,
                textAlign: TextAlign.center,
              ),
              const SizedBox(height: 12),
              Text(
                "We'll let you know when it's available for you.",
                style: AppTextStyles.body.copyWith(
                  color: AppColors.textSecondary,
                ),
                textAlign: TextAlign.center,
              ),
            ],
          ),
        ),
      ),
    );
  }
}
