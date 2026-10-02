package com.us.android.feature.dating

import com.us.android.core.designsystem.component.UsMessage
import com.us.android.feature.dating.data.DatingError
import com.us.android.feature.dating.data.code
import com.us.android.feature.dating.data.detailsAs
import com.us.android.feature.dating.network.FieldRefusalDetailsDto
import com.us.android.feature.dating.network.LocationRateLimitDetailsDto
import com.us.android.feature.dating.network.MaxLengthDetailsDto
import com.us.android.feature.dating.network.RangeDetailsDto
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
            "INVALID_INTERESTED_IN_GENDER" -> INVALID_INTERESTED_IN_GENDER
            "LOCATION_CHANGE_RATE_LIMITED" -> locationRateLimited(error, json)
            "CANDIDATE_UNAVAILABLE" -> "This person isn't available any more."
            "SPARK_RATE_LIMITED" -> "You've sent a lot of sparks today. Try again tomorrow."
            "SUPER_SPARK_LIMIT_REACHED" -> "You've used your Super Sparks for now."
            "REWIND_LIMIT_REACHED" -> "You've used today's undos."
            "REWIND_NOTHING_TO_UNDO" -> "There's no pass to undo."
            "MECHANIC_NOT_ENABLED" -> "That isn't available right now."
            "LIKED_YOU_LOCKED" -> "You'll need a Premium pass to see who sparked you."
            "SPARK_NOTE_REFUSED" ->"Notes can't include phone numbers, emails or links."
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
            // Mechanic M5 — first move.
            "OPENING_QUESTIONS_TOO_MANY" -> openingQuestionsTooMany(error, json)
            "OPENING_QUESTION_INVALID" -> openingQuestionInvalid(error, json)
            "OPENING_QUESTION_REFUSED" -> "Questions can't include phone numbers, emails or links."
            "FIRST_MOVE_NOT_PENDING" -> "This match isn't waiting for an answer from you any more."
            "OPENING_QUESTION_UNKNOWN" -> "That question isn't there any more."
            "OPENING_ANSWER_INVALID" -> openingAnswerInvalid(error, json)
            "OPENING_ANSWER_REFUSED" -> "Answers can't include phone numbers, emails or links."
            "CHAT_UNAVAILABLE" -> "Chat isn't reachable right now. Try again in a moment."
            "EXTEND_LIMIT_REACHED" -> "You've already given extra time today."
            // Mechanic M6 — profile basics and filters.
            "FILTERS_REQUIRE_PASS" -> FILTERS_REQUIRE_PASS
            "INVALID_DISTANCE_BUCKET" -> "That distance isn't available any more. Pick another one."
            "INVALID_AGE_RANGE" -> ageRange(error, json)
            "INVALID_INTENT_FILTER" -> "One of those choices isn't available any more. Check what you picked."
            "INVALID_INTEREST" -> "One of those interests isn't on the list any more. Check your picks and try again."
            "TOO_MANY_INTEREST" -> tooMany(error, json, "interests")
            "INVALID_LANGUAGE" -> "One of those languages isn't on the list any more. Check your picks and try again."
            "TOO_MANY_LANGUAGE" -> tooMany(error, json, "languages")
            "INVALID_HEIGHT" -> height(error, json)
            "INVALID_LIFESTYLE" -> "That choice isn't available any more. Pick another one."
            // Mechanic M7 — daily picks. The app retries a refused zone without
            // one, so these reach a person only if that fails too.
            "INVALID_TIMEZONE" -> "Your phone's time zone wasn't recognised. Try again later."
            "INVALID_SOURCE" -> GENERIC
            // Mechanic M8 — travel.
            "TRAVEL_REQUIRES_PASS" -> TRAVEL_REQUIRES_PASS
            "INVALID_CITY" -> "That city isn't on the list any more. Pick another one."
            "INVALID_TRAVEL_DAYS" -> travelDays(error, json)
            // Mechanic M9 — in-match extras.
            "READ_RECEIPTS_REQUIRE_PASS" -> READ_RECEIPTS_REQUIRE_PASS
            else -> GENERIC
        }
    }

    /** `403 READ_RECEIPTS_REQUIRE_PASS`: read receipts were turned on without a pass. */
    const val READ_RECEIPTS_REQUIRE_PASS = "Read receipts come with a Premium pass."

    /** `403 FILTERS_REQUIRE_PASS`: a pass filter was set without a pass. */
    const val FILTERS_REQUIRE_PASS = "These filters come with a Premium pass."

    /** `403 TRAVEL_REQUIRES_PASS`: a trip was started without a pass. */
    const val TRAVEL_REQUIRES_PASS = "Travel comes with a Premium pass."

    private fun travelDays(error: DatingError, json: Json?): String {
        val range = json?.let { error.detailsAs(it, RangeDetailsDto.serializer()) }
        return if (range != null && range.min > 0 && range.max > 0) {
            "Trips run from ${range.min} to ${range.max} days."
        } else {
            "That trip length isn't one we can set."
        }
    }

    private fun tooMany(error: DatingError, json: Json?, what: String): String {
        val max = json?.let { error.detailsAs(it, FieldRefusalDetailsDto.serializer()) }?.max?.takeIf { it > 0 }
        return if (max != null) "You can pick up to $max $what." else "That's too many $what."
    }

    private fun height(error: DatingError, json: Json?): String {
        val details = json?.let { error.detailsAs(it, FieldRefusalDetailsDto.serializer()) }
        return when {
            // Both heights were fine on their own, but the range was upside down.
            details?.field == "min_height_cm" && details.min > 0 && details.max > 0 ->
                "Pick a height range from ${details.min} to ${details.max} cm, shortest first."
            details != null && details.min > 0 && details.max > 0 -> "Height needs to be between ${details.min} and ${details.max} cm."
            else -> "That height isn't one we can save."
        }
    }

    private fun ageRange(error: DatingError, json: Json?): String {
        val range = json?.let { error.detailsAs(it, RangeDetailsDto.serializer()) }
        return if (range != null && range.min > 0 && range.max > 0) {
            "Ages run from ${range.min} to ${range.max}, youngest first."
        } else {
            "That age range doesn't work. Check both ages."
        }
    }

    fun message(error: DatingError, json: Json? = null): UsMessage = errorMessage(forError(error, json))

    const val PREMIUM_UNAVAILABLE = "Premium isn't available yet"

    /**
     * `interested_in_gender` outside `woman | man | nonbinary | everyone`. The
     * server names the field in its own code now, so the words live here rather
     * than in the preferences screen, which used to read a bare 400.
     */
    const val INVALID_INTERESTED_IN_GENDER = "That choice isn't available any more. Pick who you want to see and try again."

    private fun openingQuestionsTooMany(error: DatingError, json: Json?): String {
        val max = json?.let { error.detailsAs(it, RangeDetailsDto.serializer()) }?.max?.takeIf { it > 0 }
        return if (max != null) "You can have up to $max opening questions." else "That's too many opening questions."
    }

    private fun openingQuestionInvalid(error: DatingError, json: Json?): String {
        val max = json?.let { error.detailsAs(it, MaxLengthDetailsDto.serializer()) }?.maxLength?.takeIf { it > 0 }
        return if (max != null) "Each question needs 1 to $max characters." else "Each question needs a few words, and not too many."
    }

    private fun openingAnswerInvalid(error: DatingError, json: Json?): String {
        val max = json?.let { error.detailsAs(it, MaxLengthDetailsDto.serializer()) }?.maxLength?.takeIf { it > 0 }
        return if (max != null) "Your answer needs 1 to $max characters." else "Your answer is empty or too long."
    }

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
