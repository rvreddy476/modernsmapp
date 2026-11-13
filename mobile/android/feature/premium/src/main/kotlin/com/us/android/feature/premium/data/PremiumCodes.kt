package com.us.android.feature.premium.data

import com.us.android.core.common.error.AppError

/** The codes premium_handler.go `writePremiumError` (and the gateway) answer. */
object PremiumCodes {
    /** 503: the server's PREMIUM_ENABLED is off; every premium route answers this. */
    const val NOT_ENABLED = "PREMIUM_NOT_ENABLED"
    const val PRICE_NOT_SET = "PREMIUM_PRICE_NOT_SET"
    const val TAX_UNAVAILABLE = "PREMIUM_TAX_UNAVAILABLE"

    /** The server folds it into TAX_UNAVAILABLE today; mapped in case it is ever sent as is. */
    const val AMOUNT_MISMATCH = "PREMIUM_AMOUNT_MISMATCH"
    const val UNKNOWN_PLAN = "UNKNOWN_PLAN"
    const val PAYMENTS_UNAVAILABLE = "PREMIUM_PAYMENTS_UNAVAILABLE"
    const val SESSION_UNAVAILABLE = "PAYMENT_SESSION_UNAVAILABLE"
    const val NOT_FOUND = "NOT_FOUND"
    const val MAINTENANCE = "MAINTENANCE"
    const val BAD_REQUEST = "BAD_REQUEST"
    const val INTERNAL_ERROR = "INTERNAL_ERROR"
}

/** The server's contract code on a refusal, wherever the mapper put it. */
fun AppError.premiumCode(): String? = when (this) {
    is AppError.Server -> code
    is AppError.Unknown -> code
    is AppError.Forbidden -> code
    is AppError.NotFound -> code ?: PremiumCodes.NOT_FOUND
    is AppError.AuthFailed -> code
    is AppError.InvalidRequest -> PremiumCodes.BAD_REQUEST
    else -> null
}?.takeIf { it.isNotBlank() }

/** PREMIUM_NOT_ENABLED: the whole feature is switched off on the server. */
fun AppError.isPremiumOff(): Boolean = premiumCode() == PremiumCodes.NOT_ENABLED

/** A failure to reach the server, as opposed to the server saying no. */
val AppError.isTransport: Boolean get() = this is AppError.NoNetwork || this is AppError.Timeout

/** Every refusal in the web's words. Branches on the code, never the server's message. */
fun premiumErrorMessage(error: AppError): String {
    if (error.isTransport) return PremiumWords.NETWORK
    return when (error.premiumCode()) {
        PremiumCodes.NOT_ENABLED -> PremiumWords.NOT_ENABLED
        PremiumCodes.PRICE_NOT_SET -> PremiumWords.PRICE_NOT_SET
        PremiumCodes.TAX_UNAVAILABLE -> PremiumWords.TAX_UNAVAILABLE
        PremiumCodes.AMOUNT_MISMATCH -> PremiumWords.AMOUNT_MISMATCH
        PremiumCodes.UNKNOWN_PLAN -> PremiumWords.UNKNOWN_PLAN
        PremiumCodes.PAYMENTS_UNAVAILABLE -> PremiumWords.PAYMENTS_UNAVAILABLE
        PremiumCodes.SESSION_UNAVAILABLE -> PremiumWords.SESSION_UNAVAILABLE
        PremiumCodes.NOT_FOUND -> PremiumWords.NOT_FOUND
        PremiumCodes.MAINTENANCE -> PremiumWords.MAINTENANCE
        PremiumCodes.BAD_REQUEST -> PremiumWords.BAD_REQUEST
        else -> PremiumWords.GENERIC
    }
}

/** A document download: 404 (none, or someone else's) is "Invoice not found"; anything else, try again. */
fun invoiceErrorMessage(error: AppError): String =
    if (error is AppError.NotFound) PremiumWords.INVOICE_NOT_FOUND else PremiumWords.INVOICE_RETRY
