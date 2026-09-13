package com.us.android.core.food.repository

import com.us.android.core.food.model.OnboardingChecklist
import com.us.android.core.food.network.FoodErrorBodyDto

/**
 * The food failures a screen renders distinctly. Branch on these, never on a
 * message: messages are human-facing and get reworded.
 *
 * Codes from handler_onboarding.go writeOnboardingError and onboarding.go.
 */
sealed interface FoodError {

    /** 422 FOOD_RESTAURANT_NOT_READY: submit before every step is done. */
    data class NotReady(val missing: List<String>) : FoodError {
        val checklist: OnboardingChecklist get() = OnboardingChecklist.fromMissing(missing)
    }

    /**
     * A 422 validation failure on one input: FOOD_GSTIN_REQUIRED,
     * FOOD_GSTIN_PAN_MISMATCH, INVALID_IFSC, FOOD_DELIVERY_RADIUS_OUT_OF_RANGE, …
     * [field] is the request key (`windows[0].day_of_week` for a nested one).
     */
    data class InvalidField(val code: String, val field: String?, val message: String) : FoodError

    /** 404 FOOD_NOT_FOUND — including a restaurant the caller does not own. */
    data object NotFound : FoodError

    /** 409: submit is only allowed from DRAFT. */
    data object NotDraft : FoodError

    /** 422: accepting orders needs an ACTIVE restaurant. */
    data object NotLive : FoodError

    /** 422: accepting orders needs an approved, unexpired FSSAI document. */
    data object FssaiRequired : FoodError

    data object DocumentExpired : FoodError

    /** 503: the server has no encryption keys, so PAN and bank details are refused. */
    data object PiiNotConfigured : FoodError

    /** 503: penny-drop verification is unavailable. */
    data object BankVerificationUnavailable : FoodError

    /** 400 INVALID_BODY: a client bug, never a user error. */
    data object InvalidBody : FoodError

    data object Unauthorized : FoodError

    /** A 404 with no code: this server does not have the route yet. */
    data object NotAvailable : FoodError

    data class Network(val cause: Throwable?) : FoodError

    data class Unexpected(val status: Int?, val code: String?, val message: String?) : FoodError

    companion object {
        /** Maps an HTTP status and a parsed error body onto a [FoodError]. Pure. */
        @Suppress("CyclomaticComplexMethod")
        fun from(status: Int, body: FoodErrorBodyDto?): FoodError {
            val code = body?.code?.takeIf { it.isNotBlank() }
            val field = body?.details?.field
            val message = body?.message.orEmpty()
            return when (code) {
                "FOOD_RESTAURANT_NOT_READY" -> NotReady(body?.details?.missing.orEmpty())
                "FOOD_RESTAURANT_NOT_DRAFT" -> NotDraft
                "FOOD_RESTAURANT_NOT_LIVE" -> NotLive
                "FOOD_FSSAI_REQUIRED" -> FssaiRequired
                "FOOD_DOCUMENT_EXPIRED" -> DocumentExpired
                "PII_NOT_CONFIGURED" -> PiiNotConfigured
                "FOOD_BANK_VERIFICATION_UNAVAILABLE" -> BankVerificationUnavailable
                "INVALID_BODY" -> InvalidBody
                "FOOD_NOT_FOUND" -> NotFound
                null -> when (status) {
                    HTTP_UNAUTHORIZED -> Unauthorized
                    HTTP_NOT_FOUND -> NotAvailable
                    else -> Unexpected(status, null, message.ifEmpty { null })
                }
                else -> when {
                    status == HTTP_UNAUTHORIZED -> Unauthorized
                    status == HTTP_UNPROCESSABLE || field != null -> InvalidField(code, field, message)
                    else -> Unexpected(status, code, message)
                }
            }
        }

        private const val HTTP_UNAUTHORIZED = 401
        private const val HTTP_NOT_FOUND = 404
        private const val HTTP_UNPROCESSABLE = 422
    }
}

/** A result carrying a typed failure rather than a thrown exception. */
sealed interface FoodResult<out T> {
    data class Success<T>(val value: T) : FoodResult<T>
    data class Failure(val error: FoodError) : FoodResult<Nothing>
}

inline fun <T, R> FoodResult<T>.map(transform: (T) -> R): FoodResult<R> = when (this) {
    is FoodResult.Success -> FoodResult.Success(transform(value))
    is FoodResult.Failure -> this
}
