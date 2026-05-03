import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:atpost_app/data/repositories/pulse_repository.dart';
import 'package:atpost_app/providers/user_provider.dart';
import 'package:atpost_app/services/pulse_api_client.dart';
import 'package:atpost_app/services/pulse_crash_breadcrumbs.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Sprint 6 — master Pulse rollout gate.
///
/// Three pieces glue together here:
///
///   1. `pulseEnabledProvider` — resolves the master feature flag
///      `pulse_enabled_master` from feature-flag-service. While the flag is
///      OFF, the entire Pulse experience is hidden.
///
///   2. `pulseCityGateProvider` — Bengaluru-only city gate. The v1 launch
///      ships only to Bengaluru / Bangalore. Profiles in any other city see
///      a "coming to your city soon" + waitlist screen instead of the
///      normal Pulse surface.
///
///   3. `PulseGate` widget — wraps a Pulse screen and renders the right
///      empty state when the gate fails. Use it like `PulseGate(child: ...)`
///      around a Pulse route, or call the providers directly to flip
///      navigation entries (e.g. hiding a bottom-nav tab).
///
/// All three knobs are opaque to the rest of the codebase — the launch
/// runbook says to flip flags via feature-flag-service, never via code.

/// Hard-coded list of cities allowed for the v1 soft launch. Case-insensitive.
/// Adding a city requires a deploy because the launch runbook calls out
/// "every flag flip is a deploy event".
const List<String> kPulseAllowedCitiesV1 = <String>[
  'Bengaluru',
  'Bangalore',
];

/// Master switch — `pulse_enabled_master` in feature-flag-service.
/// Errors default to OFF (safer to leave Pulse hidden than expose a
/// half-built experience).
final pulseEnabledProvider = FutureProvider<bool>((ref) async {
  final api = ref.watch(pulseApiClientProvider);
  try {
    final response = await api.get(
      '/v1/feature-flags/check',
      queryParameters: {'key': 'pulse_enabled_master'},
    );
    final body = response.data;
    if (body is Map<String, dynamic>) {
      final data = body['data'] is Map<String, dynamic>
          ? body['data'] as Map<String, dynamic>
          : body;
      final enabled = data['enabled'];
      if (enabled is bool) return enabled;
    }
    return false;
  } on DioException catch (e, st) {
    PulseBreadcrumbs.error(
      'pulse_enabled flag fetch failed',
      error: e,
      stackTrace: st,
    );
    return false;
  } catch (e, st) {
    PulseBreadcrumbs.error(
      'pulse_enabled flag fetch failed',
      error: e,
      stackTrace: st,
    );
    return false;
  }
});

/// Result of the city gate. Composed at one point so screens don't have to
/// re-implement the case-insensitive membership check.
class PulseCityGateState {
  const PulseCityGateState({
    required this.allowed,
    required this.userCity,
  });

  /// True when the user's city is in the v1 allowed list.
  final bool allowed;

  /// The user's stored city (empty string when unset).
  final String userCity;

  /// `null` when the user has no city; useful for "Set your city" prompts.
  String? get displayCity => userCity.isEmpty ? null : userCity;
}

/// Determines whether the current user's city is in the v1 allowed list.
/// Reads `currentUserProvider.profile.city` first, then falls back to
/// dating-profile city via the Pulse repository.
final pulseCityGateProvider = FutureProvider<PulseCityGateState>((ref) async {
  String userCity = '';
  try {
    final user = await ref.watch(currentUserProvider.future);
    if (user.location != null && user.location!.isNotEmpty) {
      userCity = user.location!;
    }
  } catch (_) {
    // ignore — fall through to dating profile fetch
  }
  if (userCity.isEmpty) {
    try {
      final profile = await ref.watch(pulseRepositoryProvider).getProfile();
      if (profile != null) {
        // PulseProfile keeps city in a few possible places depending on
        // sprint vintage. Prefer the explicit `city` field if present.
        final dyn = profile as dynamic;
        try {
          final c = dyn.city;
          if (c is String && c.isNotEmpty) userCity = c;
        } catch (_) {}
      }
    } catch (_) {
      // ignore
    }
  }
  final allowed = _cityMatches(userCity, kPulseAllowedCitiesV1);
  return PulseCityGateState(allowed: allowed, userCity: userCity);
});

bool _cityMatches(String city, List<String> allowed) {
  if (city.isEmpty) return false;
  final lower = city.trim().toLowerCase();
  for (final c in allowed) {
    if (lower == c.toLowerCase()) return true;
  }
  return false;
}

/// `PulseGate` — wraps a Pulse screen.
///
/// Renders, in order of priority:
///   1. Loading shimmer while the master flag resolves.
///   2. "Pulse is rolling out city by city" when the master flag is OFF.
///   3. The city-gate "Coming soon — join the waitlist" screen when the
///      master flag is ON but the user is outside the allowed city list.
///   4. The wrapped child otherwise.
class PulseGate extends ConsumerWidget {
  const PulseGate({super.key, required this.child});

  final Widget child;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final flagAsync = ref.watch(pulseEnabledProvider);

    return flagAsync.when(
      loading: () => const _PulseGateLoading(),
      error: (_, _) => const _PulseRolloutEmpty(),
      data: (enabled) {
        if (!enabled) {
          return const _PulseRolloutEmpty();
        }
        final cityAsync = ref.watch(pulseCityGateProvider);
        return cityAsync.when(
          loading: () => const _PulseGateLoading(),
          error: (_, _) => child, // fail-open on city read error
          data: (state) {
            if (state.allowed) return child;
            return PulseCityGatedScreen(userCity: state.displayCity);
          },
        );
      },
    );
  }
}

class _PulseGateLoading extends StatelessWidget {
  const _PulseGateLoading();

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      body: const Center(
        child: CircularProgressIndicator(
          color: AppColors.postbookPrimary,
        ),
      ),
    );
  }
}

class _PulseRolloutEmpty extends StatelessWidget {
  const _PulseRolloutEmpty();

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Pulse', style: AppTextStyles.h2),
      ),
      body: Padding(
        padding: AppSpacing.pagePadding,
        child: Center(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Icon(
                Icons.favorite_outline,
                size: 64,
                color: AppColors.postbookPrimary,
              ),
              const SizedBox(height: 24),
              Text(
                'Pulse is rolling out city by city.',
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

/// City-gated waitlist screen. Captures email and posts it via
/// `pulseRepository.joinWaitlist`. Backend wires this in Sprint 7.
class PulseCityGatedScreen extends ConsumerStatefulWidget {
  const PulseCityGatedScreen({super.key, this.userCity});

  final String? userCity;

  @override
  ConsumerState<PulseCityGatedScreen> createState() =>
      _PulseCityGatedScreenState();
}

class _PulseCityGatedScreenState
    extends ConsumerState<PulseCityGatedScreen> {
  final _emailController = TextEditingController();
  bool _submitting = false;
  bool _submitted = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    PulseBreadcrumbs.add(
      'pulse.city_gate.shown',
      data: {'city': widget.userCity ?? ''},
    );
  }

  @override
  void dispose() {
    _emailController.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    final email = _emailController.text.trim();
    if (email.isEmpty || !email.contains('@')) {
      setState(() => _error = 'Please enter a valid email.');
      return;
    }
    setState(() {
      _submitting = true;
      _error = null;
    });
    try {
      await ref.read(pulseRepositoryProvider).joinWaitlist(
            city: widget.userCity ?? '',
            email: email,
          );
      if (!mounted) return;
      setState(() {
        _submitting = false;
        _submitted = true;
      });
      PulseBreadcrumbs.add(
        'pulse.waitlist.joined',
        data: {'city': widget.userCity ?? ''},
      );
    } catch (e, st) {
      PulseBreadcrumbs.error(
        'pulse waitlist submit failed',
        error: e,
        stackTrace: st,
      );
      if (!mounted) return;
      setState(() {
        _submitting = false;
        _error = 'Could not save you to the waitlist. Try again later.';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final city = widget.userCity ?? 'your city';
    return Scaffold(
      backgroundColor: AppColors.bgPrimary,
      appBar: AppBar(
        backgroundColor: AppColors.bgPrimary,
        elevation: 0,
        title: Text('Pulse', style: AppTextStyles.h2),
      ),
      body: SingleChildScrollView(
        padding: AppSpacing.pagePadding,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            const SizedBox(height: 32),
            const Icon(
              Icons.location_on_outlined,
              size: 64,
              color: AppColors.postbookPrimary,
            ),
            const SizedBox(height: 24),
            Text(
              'Coming to $city soon.',
              style: AppTextStyles.h2,
              textAlign: TextAlign.center,
            ),
            const SizedBox(height: 12),
            Text(
              "Pulse is launching one city at a time. We'll prioritise "
              'opening up next where the most people are waiting.',
              style: AppTextStyles.body.copyWith(
                color: AppColors.textSecondary,
              ),
              textAlign: TextAlign.center,
            ),
            const SizedBox(height: 32),
            if (_submitted)
              Container(
                padding: const EdgeInsets.all(16),
                decoration: BoxDecoration(
                  color: AppColors.bgCard,
                  borderRadius: BorderRadius.circular(
                    AppSpacing.radiusLarge,
                  ),
                  border: Border.all(color: AppColors.borderSubtle),
                ),
                child: Text(
                  "You're on the waitlist. We'll email you as soon as "
                  "Pulse opens for $city.",
                  style: AppTextStyles.body.copyWith(
                    color: AppColors.textPrimary,
                  ),
                  textAlign: TextAlign.center,
                ),
              )
            else ...[
              Text(
                'Add yourself to the waitlist.',
                style: AppTextStyles.body,
                textAlign: TextAlign.center,
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _emailController,
                keyboardType: TextInputType.emailAddress,
                style: AppTextStyles.body.copyWith(
                  color: AppColors.textPrimary,
                ),
                decoration: InputDecoration(
                  hintText: 'you@example.com',
                  hintStyle: AppTextStyles.bodySmall.copyWith(
                    color: AppColors.textMuted,
                  ),
                  filled: true,
                  fillColor: AppColors.bgCard,
                  errorText: _error,
                  border: OutlineInputBorder(
                    borderRadius: BorderRadius.circular(
                      AppSpacing.radiusLarge,
                    ),
                    borderSide: BorderSide(color: AppColors.borderSubtle),
                  ),
                ),
              ),
              const SizedBox(height: 16),
              FilledButton(
                onPressed: _submitting ? null : _submit,
                style: FilledButton.styleFrom(
                  backgroundColor: AppColors.postbookPrimary,
                  padding: const EdgeInsets.symmetric(vertical: 14),
                ),
                child: _submitting
                    ? const SizedBox(
                        width: 18,
                        height: 18,
                        child: CircularProgressIndicator(
                          strokeWidth: 2,
                          color: Colors.white,
                        ),
                      )
                    : const Text('Join the waitlist'),
              ),
            ],
          ],
        ),
      ),
    );
  }
}

/// Helper used by the cohort-gate codepath in `pulse_discover_screen.dart`.
/// The dating-service can answer pulse/today with `cohort_gated: true` —
/// when that happens we render the same city-gated screen so users see a
/// consistent "coming soon" experience whether the gate is geographic or
/// cohort-based.
class PulseCohortGatedScreen extends StatelessWidget {
  const PulseCohortGatedScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return const PulseCityGatedScreen(userCity: null);
  }
}
