package com.us.android.feature.dating

import com.us.android.core.designsystem.component.UsMessage
import com.us.android.feature.dating.data.DatingError
import com.us.android.feature.dating.data.code
import com.us.android.feature.dating.data.detailsAs
import com.us.android.feature.dating.network.LocationRateLimitDetailsDto
import com.us.android.feature.dating.ui.errorMessage
import kotlinx.serialization.json.Json

/**
 * The words for a server refusal, by stable CODE. A code this table does not
 * know falls back to a calm generic line rather than the server's message,
 * which is written for developers.
 */
object DatingCopy {

    const val GENERIC = "Something went wrong. Please try again."
    const val NETWORK = "Check your connection and try again."
    const val NOT_AVAILABLE_TITLE = "Dating isn't available for your account yet"
    const val NOT_AVAILABLE_BODY = "We're opening Dating to a small group first. When it's ready for you, it'll appear here."

    @Suppress("CyclomaticComplexMethod")
    fun forError(error: DatingError, json: Json? = null): String = when (error) {
        DatingError.NotAvailable -> NOT_AVAILABLE_TITLE
        DatingError.Unauthorized -> "Your session ended. Sign in again."
        is DatingError.Network -> NETWORK
        is DatingError.ConsentRequired -> "We need your consent before saving that."
        is DatingError.Unexpected -> GENERIC
        is DatingError.Refused -> when (error.code) {
            "AGE_REQUIRED" -> "Dating needs a verified birth date showing you're 18 or older. Add it to your Momentum account first."
            "IDENTITY_UNAVAILABLE" -> "We couldn't confirm your details right now. Try again in a moment."
            "PII_NOT_CONFIGURED" -> "This can't be saved right now. Try again later."
            "INVALID_LOCATION" -> "That location didn't look right. Try again, or type your city."
            "LOCATION_CHANGE_RATE_LIMITED" -> locationRateLimited(error, json)
            "CANDIDATE_UNAVAILABLE" -> "This person isn't available any more."
            "SPARK_RATE_LIMITED" -> "You've sent a lot of sparks today. Try again tomorrow."
            "SPARK_NOTE_REFUSED" -> "Notes can't include phone numbers, emails or links."
            "EXPLAIN_RATE_LIMITED" -> "Try again later."
            "PROFILE_TRANSITION_NOT_ALLOWED", "PROFILE_STATUS_CONFLICT" -> "Your profile can't do that right now."
            "PHOTO_LIMIT_REACHED" -> "You can have up to 6 photos. Remove one to add another."
            "PHOTO_MEDIA_NOT_READY" -> "That photo is still processing. Try again in a moment."
            "PHOTO_MEDIA_UNSUPPORTED" -> "That file type isn't supported. Choose a JPG or PNG photo."
            "PHOTO_MEDIA_NOT_FOUND", "PHOTO_ALREADY_ATTACHED" -> "That photo couldn't be added. Try another."
            "PHOTO_MEDIA_UNAVAILABLE" -> "Photos are unavailable right now. Try again later."
            "REPORT_RATE_LIMITED" -> "You've sent a lot of reports today. Our team is reviewing them."
            "INVALID_REPORT_REASON", "INVALID_REPORT_EVIDENCE" -> "That report couldn't be sent. Check the details and try again."
            "REPORT_TARGET_MISMATCH" -> "That report couldn't be sent."
            "TRUSTED_CONTACT_LIMIT" -> "You can have up to 3 trusted contacts. Remove one to add another."
            "TRUSTED_CONTACT_NOT_ELIGIBLE" -> "A trusted contact must be one of your matches or connections."
            "CONNECTION_CHECK_UNAVAILABLE" -> "We couldn't check that right now. Try again in a moment."
            "SHARE_RECIPIENT_NOT_ALLOWED" -> "You can share your location only with a match or a trusted contact."
            "PREMIUM_UNAVAILABLE", "PREMIUM_PAYMENTS_UNAVAILABLE" -> PREMIUM_UNAVAILABLE
            "PREMIUM_PAYMENTS_REFUSED" -> "The payment couldn't be started. Try again later."
            "IDEMPOTENCY_KEY_REUSED", "PURCHASE_INTENT_CONFLICT" -> "That purchase changed. Start again."
            "FORBIDDEN" -> "You can't do that right now."
            else -> GENERIC
        }
    }

    fun message(error: DatingError, json: Json? = null): UsMessage = errorMessage(forError(error, json))

    const val PREMIUM_UNAVAILABLE = "Premium isn't available yet"

    private fun locationRateLimited(error: DatingError, json: Json?): String {
        val limits = json?.let { error.detailsAs(it, LocationRateLimitDetailsDto.serializer()) }
        return if (limits != null && limits.minIntervalMinutes > 0 && limits.maxChangesPerDay > 0) {
            "You can change your location once every ${limits.minIntervalMinutes} minutes, up to " +
                "${limits.maxChangesPerDay} times a day. Try again later."
        } else {
            "You've changed your location a lot recently. Try again later."
        }
    }
}
