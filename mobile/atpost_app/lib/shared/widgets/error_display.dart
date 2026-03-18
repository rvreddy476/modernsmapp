import 'package:atpost_app/core/errors/app_exception.dart';
import 'package:atpost_app/core/theme/app_colors.dart';
import 'package:atpost_app/core/theme/app_spacing.dart';
import 'package:atpost_app/core/theme/app_text_styles.dart';
import 'package:flutter/material.dart';

/// Inline error banner with icon, message, and retry button.
///
/// Use inside lists or columns where you want a compact error indicator.
class ErrorBanner extends StatelessWidget {
  const ErrorBanner({
    super.key,
    required this.message,
    this.onRetry,
  });

  final String message;
  final VoidCallback? onRetry;

  /// Creates an [ErrorBanner] from an [AppException].
  factory ErrorBanner.fromException(AppException exception, {VoidCallback? onRetry}) {
    return ErrorBanner(message: exception.userMessage, onRetry: onRetry);
  }

  @override
  Widget build(BuildContext context) {
    return Container(
      margin: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppColors.statusError.withValues(alpha: 0.08),
        borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
        border: Border.all(color: AppColors.statusError.withValues(alpha: 0.2)),
      ),
      child: Row(
        children: [
          Icon(Icons.error_outline, color: AppColors.statusError, size: 20),
          const SizedBox(width: 10),
          Expanded(
            child: Text(
              message,
              style: AppTextStyles.bodySmall.copyWith(color: AppColors.textSecondary),
            ),
          ),
          if (onRetry != null) ...[
            const SizedBox(width: 8),
            GestureDetector(
              onTap: onRetry,
              child: Container(
                padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
                decoration: BoxDecoration(
                  color: AppColors.statusError.withValues(alpha: 0.12),
                  borderRadius: BorderRadius.circular(AppSpacing.radiusSmall),
                ),
                child: Text(
                  'Retry',
                  style: AppTextStyles.label.copyWith(color: AppColors.statusError),
                ),
              ),
            ),
          ],
        ],
      ),
    );
  }
}

/// Full-screen centered error with icon, message, and retry button.
///
/// Use in `AsyncValue.when()` error branches as a replacement for plain `Text(...)`.
class FullScreenError extends StatelessWidget {
  const FullScreenError({
    super.key,
    required this.message,
    this.onRetry,
  });

  final String message;
  final VoidCallback? onRetry;

  /// Creates a [FullScreenError] from an [AppException].
  factory FullScreenError.fromException(AppException exception, {VoidCallback? onRetry}) {
    return FullScreenError(message: exception.userMessage, onRetry: onRetry);
  }

  /// Creates a [FullScreenError] from any error, extracting userMessage if it's an [AppException].
  factory FullScreenError.fromError(Object error, {VoidCallback? onRetry}) {
    final message = error is AppException ? error.userMessage : 'Something went wrong.';
    return FullScreenError(message: message, onRetry: onRetry);
  }

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.cloud_off_outlined, size: 48, color: AppColors.textMuted),
            const SizedBox(height: 16),
            Text(
              message,
              textAlign: TextAlign.center,
              style: AppTextStyles.body.copyWith(color: AppColors.textSecondary),
            ),
            if (onRetry != null) ...[
              const SizedBox(height: 20),
              GestureDetector(
                onTap: onRetry,
                child: Container(
                  padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 10),
                  decoration: BoxDecoration(
                    gradient: AppColors.postbookGradient,
                    borderRadius: BorderRadius.circular(AppSpacing.radiusMedium),
                  ),
                  child: Text(
                    'Try Again',
                    style: AppTextStyles.label.copyWith(color: Colors.white),
                  ),
                ),
              ),
            ],
          ],
        ),
      ),
    );
  }
}

/// Static helper to show error SnackBars with consistent styling.
class ErrorSnackBar {
  const ErrorSnackBar._();

  /// Shows a SnackBar with the [AppException]'s user-facing message.
  static void show(BuildContext context, AppException exception) {
    showMessage(context, exception.userMessage);
  }

  /// Shows a SnackBar with a custom error message.
  static void showMessage(BuildContext context, String message) {
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Row(
          children: [
            const Icon(Icons.error_outline, color: Colors.white, size: 18),
            const SizedBox(width: 8),
            Expanded(child: Text(message)),
          ],
        ),
        backgroundColor: AppColors.statusError,
        behavior: SnackBarBehavior.floating,
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(10)),
        margin: const EdgeInsets.all(16),
        duration: const Duration(seconds: 4),
      ),
    );
  }

  /// Shows a SnackBar from any error, extracting userMessage if it's an [AppException].
  static void showError(BuildContext context, Object error) {
    final message = error is AppException ? error.userMessage : 'Something went wrong.';
    showMessage(context, message);
  }
}
