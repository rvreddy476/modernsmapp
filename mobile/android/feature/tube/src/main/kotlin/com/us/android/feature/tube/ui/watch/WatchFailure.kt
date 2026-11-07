package com.us.android.feature.tube.ui.watch

import com.us.android.core.common.error.AppError
import com.us.android.core.feed.data.serverCode

/**
 * Why a video could not be opened, in words the viewer can act on (audit
 * L5, 2026-11-07). The age gate (post-service `age_gate.go`) answers three
 * codes, and each used to read as the generic failure: a viewer under 18,
 * a viewer with no date of birth and a signed-out one all saw "We couldn't
 * load this video". The gate itself is the server's; this is only what is
 * said. Anything else keeps the generic sentence.
 */
internal fun watchFailure(error: AppError?): WatchContent.Failed = when {
    error is AppError.NoNetwork || error is AppError.Timeout -> WatchContent.Failed(OFFLINE)
    else -> when (error?.serverCode() ?: (error as? AppError.AuthFailed)?.code) {
        CODE_AGE_SIGN_IN -> WatchContent.Failed(AGE_SIGN_IN)
        CODE_AGE_RESTRICTED -> WatchContent.Failed(AGE_RESTRICTED)
        CODE_AGE_UNVERIFIED -> WatchContent.Failed(AGE_UNVERIFIED, WatchFailureAction.CONFIRM_BIRTH_DATE)
        else -> WatchContent.Failed(LOAD_FAILED)
    }
}

internal const val CODE_AGE_SIGN_IN = "AGE_RESTRICTED_SIGN_IN"
internal const val CODE_AGE_RESTRICTED = "AGE_RESTRICTED"
internal const val CODE_AGE_UNVERIFIED = "AGE_UNVERIFIED"

internal const val LOAD_FAILED = "We couldn't load this video."
internal const val OFFLINE = "You're offline. Connect to watch this video."
internal const val AGE_SIGN_IN = "Sign in to confirm your age to watch this video."
internal const val AGE_RESTRICTED = "This video is age-restricted, so it isn't available on your account."
internal const val AGE_UNVERIFIED = "Confirm your date of birth to watch age-restricted videos."
