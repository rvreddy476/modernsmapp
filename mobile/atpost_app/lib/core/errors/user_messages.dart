/// Turns backend error codes into sentences an ordinary person understands.
///
/// The backend answers failures as `{"error":{"code":"CONSENT_REQUIRED",
/// "message":"..."}}`. Those server messages are written for developers —
/// accurate, but phrased around rules rather than around what the person
/// holding the phone should do next.
///
/// Every string here follows three rules:
///   * say what happened, in words with no jargon and no status codes;
///   * say what to do next, because a dead end is what makes people quit;
///   * never blame the user.
///
/// Unknown codes fall back to the server's own message, which is still far
/// better than "DioException [bad response] ... status code of 409".
class UserMessages {
  const UserMessages._();

  static const _byCode = <String, String>{
    // ── Registration ────────────────────────────────────────────────────
    'USER_EXISTS':
        'This email is already registered. Try signing in instead.',
    'EMAIL_EXISTS':
        'This email is already registered. Try signing in instead.',
    'CONSENT_REQUIRED':
        'Please tick the box to accept the Terms and Privacy Policy.',
    'INVALID_DOB': 'Please enter your date of birth.',
    'UNDERAGE': 'You need to be at least 13 years old to join.',
    'WEAK_PASSWORD':
        'Please choose a longer password — at least 8 characters, with a number.',
    'EMAIL_REQUIRED': 'Please enter your email address.',
    'INVALID_EMAIL': 'That email address does not look right.',

    // ── Verification ────────────────────────────────────────────────────
    'EMAIL_NOT_VERIFIED':
        'Please verify your email first. Check your inbox for the 6-digit code.',
    'INVALID_CODE': 'That code is not correct. Please check and try again.',
    'CODE_EXPIRED':
        'That code has expired. Tap "Resend code" to get a new one.',

    // ── Sign in ─────────────────────────────────────────────────────────
    'INVALID_CREDENTIALS':
        'Email or password is incorrect. Please try again.',
    'ACCOUNT_LOCKED':
        'Your account is locked for a short while after too many attempts. Please wait and try again.',
    'ACCOUNT_SUSPENDED':
        'This account has been suspended. Please contact support.',
  };

  /// Falls back by HTTP status when the server sends no code we recognise.
  static const _byStatus = <int, String>{
    401: 'Email or password is incorrect. Please try again.',
    403: 'Please verify your email first. Check your inbox for the 6-digit code.',
    404: 'We could not find what you were looking for.',
    409: 'This email is already registered. Try signing in instead.',
    429: 'Too many attempts. Please wait a moment and try again.',
    500: 'Something went wrong on our side. Please try again in a moment.',
    502: 'We could not reach the server. Please try again in a moment.',
    503: 'The service is busy right now. Please try again in a moment.',
  };

  /// Resolution order: exact code, then status, then the server's own message,
  /// then a last-resort generic. The server message ranks above the generic
  /// because a specific sentence written by the backend still beats "something
  /// went wrong".
  static String resolve({
    String? code,
    int? statusCode,
    String? serverMessage,
  }) {
    final byCode = code == null ? null : _byCode[code.toUpperCase()];
    if (byCode != null) return byCode;

    final byStatus = statusCode == null ? null : _byStatus[statusCode];
    if (byStatus != null) return byStatus;

    if (serverMessage != null && serverMessage.trim().isNotEmpty) {
      // Guard against leaking a raw Dio dump into the UI. Those always mention
      // the exception type, and are never something a user should read.
      final looksTechnical = serverMessage.contains('DioException') ||
          serverMessage.contains('status code of');
      if (!looksTechnical) return serverMessage;
    }

    return 'Something went wrong. Please try again.';
  }
}
