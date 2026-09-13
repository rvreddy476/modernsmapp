package com.us.android.feature.rider.digilocker

import com.us.android.core.food.network.DeliveryKycDto
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.code
import java.net.URI
import java.net.URLDecoder

/**
 * What came back on the DigiLocker return link.
 *
 * food-service's public return route (`GET /v1/food/public/digilocker/return`)
 * 302s the browser to `FOOD_RIDER_APP_LINK_URL` with `code`, `state`, `error`
 * and `error_description` passed through unchanged (service.DigiLockerAppLinkURL).
 */
data class DigiLockerReturn(
    val code: String?,
    val state: String?,
    val error: String?,
    val errorDescription: String?,
)

/**
 * The link shapes Feast Rider accepts as a DigiLocker return.
 *
 *  1. `http(s)://<any host>/rider/digilocker/return?…` — what food-service can
 *     redirect to TODAY. `digilocker.SettingsFromEnv` refuses any
 *     `FOOD_RIDER_APP_LINK_URL` that is not an absolute http(s) URL with a host,
 *     and production additionally requires https. The manifest registers the
 *     host per flavour (see :app-rider's build file).
 *  2. `feastrider://digilocker/return?…` — the dev custom scheme. Android opens
 *     it without App Link verification, but food-service CANNOT redirect to it
 *     until checkURL allows a custom scheme outside production (backend gap).
 *
 * Parsed with java.net.URI so it is testable on the JVM.
 */
object DigiLockerReturnLink {
    const val CUSTOM_SCHEME = "feastrider"
    const val CUSTOM_HOST = "digilocker"
    const val CUSTOM_PATH = "/return"
    const val APP_LINK_PATH = "/rider/digilocker/return"

    fun parse(link: String?): DigiLockerReturn? {
        val uri = runCatching { URI(link?.trim().orEmpty()) }.getOrNull() ?: return null
        val scheme = uri.scheme?.lowercase() ?: return null
        val matches = when (scheme) {
            CUSTOM_SCHEME -> uri.host.equals(CUSTOM_HOST, ignoreCase = true) && uri.path == CUSTOM_PATH
            "http", "https" -> !uri.host.isNullOrEmpty() && uri.path == APP_LINK_PATH
            else -> false
        }
        if (!matches) return null
        val query = parseQuery(uri.rawQuery)
        return DigiLockerReturn(
            code = query["code"],
            state = query["state"],
            error = query["error"],
            errorDescription = query["error_description"],
        )
    }

    private fun parseQuery(raw: String?): Map<String, String> {
        if (raw.isNullOrEmpty()) return emptyMap()
        return raw.split('&').mapNotNull { pair ->
            val eq = pair.indexOf('=')
            if (eq <= 0) return@mapNotNull null
            val key = decode(pair.substring(0, eq)) ?: return@mapNotNull null
            val value = decode(pair.substring(eq + 1)) ?: return@mapNotNull null
            key to value
        }.filter { it.second.isNotEmpty() }.toMap()
    }

    private fun decode(part: String): String? = runCatching { URLDecoder.decode(part, "UTF-8") }.getOrNull()
}

/** The verification this device started: the `state` DigiLocker must echo. */
data class PendingDigiLocker(val state: String, val expiresAt: String)

/** Where [PendingDigiLocker] survives the browser round trip (the process may die meanwhile). */
interface DigiLockerStateStore {
    fun save(pending: PendingDigiLocker)
    fun load(): PendingDigiLocker?
    fun clear()
}

/** What the app does with a return link, before any network call. */
sealed interface ReturnCheck {
    /** Post `{state, code}` to the callback. */
    data class Complete(val state: String, val code: String) : ReturnCheck

    /** No verification was started on this device (or it was already finished). */
    data object NothingPending : ReturnCheck

    /**
     * The link's state is not the one this device was given. Never posted: it
     * is either a stale link or someone else's, and the callback would only
     * burn the real one.
     */
    data object StateMismatch : ReturnCheck

    /** DigiLocker came back with `error` (the rider cancelled, or consent failed). */
    data class Declined(val error: String) : ReturnCheck

    data object MissingCode : ReturnCheck
}

/**
 * The state echo. DigiLocker's `state` must equal the one `start` returned and
 * this device saved; only then is the one-time code posted.
 */
object DigiLockerReturnPolicy {
    fun check(link: DigiLockerReturn, pending: PendingDigiLocker?): ReturnCheck {
        if (pending == null) return ReturnCheck.NothingPending
        if (link.state.isNullOrEmpty() || link.state != pending.state) return ReturnCheck.StateMismatch
        link.error?.let { return ReturnCheck.Declined(it) }
        val code = link.code?.takeIf { it.isNotBlank() } ?: return ReturnCheck.MissingCode
        return ReturnCheck.Complete(state = pending.state, code = code)
    }
}

/** The callback's result, as the DigiLocker screen shows it. */
sealed interface DigiLockerOutcome {
    data class Verified(val kyc: DeliveryKycDto) : DigiLockerOutcome

    /** 403 FOOD_DIGILOCKER_STATE_NOT_YOURS: started under another account on this device. */
    data object StartedByAnotherAccount : DigiLockerOutcome

    /** 409 FOOD_DIGILOCKER_STATE_USED. */
    data object LinkUsed : DigiLockerOutcome

    /** 410 FOOD_DIGILOCKER_STATE_EXPIRED. */
    data object LinkExpired : DigiLockerOutcome

    /** 422 FOOD_DIGILOCKER_STATE_INVALID. */
    data object LinkInvalid : DigiLockerOutcome

    /** 502 FOOD_DIGILOCKER_PROVIDER_FAILED. */
    data object ProviderFailed : DigiLockerOutcome

    /** 409 FOOD_DOCUMENT_NUMBER_IN_USE: the licence or RC already backs another rider. */
    data object DocumentInUse : DigiLockerOutcome

    /** 503 FOOD_DIGILOCKER_NOT_CONFIGURED or PII_NOT_CONFIGURED. */
    data object NotAvailable : DigiLockerOutcome

    data class Failed(val error: FoodError) : DigiLockerOutcome

    /** Whether the only way forward is a new start. */
    val startAgain: Boolean
        get() = this == LinkUsed || this == LinkExpired || this == LinkInvalid ||
            this == ProviderFailed || this == StartedByAnotherAccount

    companion object {
        fun from(result: FoodResult<DeliveryKycDto>): DigiLockerOutcome = when (result) {
            is FoodResult.Success -> Verified(result.value)
            is FoodResult.Failure -> fromError(result.error)
        }

        fun fromError(error: FoodError): DigiLockerOutcome = when {
            error == FoodError.PiiNotConfigured -> NotAvailable
            else -> when (error.code) {
                "FOOD_DIGILOCKER_STATE_NOT_YOURS" -> StartedByAnotherAccount
                "FOOD_DIGILOCKER_STATE_USED" -> LinkUsed
                "FOOD_DIGILOCKER_STATE_EXPIRED" -> LinkExpired
                "FOOD_DIGILOCKER_STATE_INVALID" -> LinkInvalid
                "FOOD_DIGILOCKER_PROVIDER_FAILED" -> ProviderFailed
                "FOOD_DOCUMENT_NUMBER_IN_USE" -> DocumentInUse
                "FOOD_DIGILOCKER_NOT_CONFIGURED" -> NotAvailable
                else -> Failed(error)
            }
        }
    }
}
