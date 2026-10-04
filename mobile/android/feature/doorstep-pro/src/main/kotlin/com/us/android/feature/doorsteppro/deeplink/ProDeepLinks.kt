package com.us.android.feature.doorsteppro.deeplink

import kotlinx.coroutines.channels.BufferOverflow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.asSharedFlow
import java.net.URI
import java.net.URLDecoder
import javax.inject.Inject
import javax.inject.Singleton

/** Where a link or a push takes the professional. */
sealed interface ProDeepLink {
    data object Home : ProDeepLink

    /** `doorstep-pro://offers` — the open offers on Home. */
    data object Offers : ProDeepLink

    data class Offer(val offerId: String) : ProDeepLink

    data class Job(val bookingId: String) : ProDeepLink

    data class JobChat(val bookingId: String) : ProDeepLink

    data object Onboarding : ProDeepLink

    data object PoliceCertificate : ProDeepLink

    data object Account : ProDeepLink

    data object Earnings : ProDeepLink

    data class DigiLocker(val link: DigiLockerReturn) : ProDeepLink
}

/**
 * The links Doorstep Pro accepts (contracts/doorstep/asyncapi.yaml
 * `x-push-types.doorstep_pro` deeplinks, and the DigiLocker return):
 *
 *     doorstep-pro://home | offers | offers/<id> | jobs/<id> | jobs/<id>/chat
 *                  | onboarding | onboarding/police-certificate | account | earnings
 *     https://<app link host>/doorstep-pro/digilocker?code=…&state=…
 *     doorstep-pro://digilocker?code=…&state=…     (dev builds only)
 *
 * Ids are uuids in the contract; anything with an unexpected character is
 * refused, never forwarded into navigation. Parsed with java.net.URI so it is
 * testable on the JVM.
 */
object ProDeepLinks {
    const val SCHEME = "doorstep-pro"

    private val ID = Regex("^[A-Za-z0-9-]{1,64}$")

    /**
     * An intent's data URI or a push `deep_link`. [allowCustomSchemeDigiLocker]
     * is true only in dev builds: any app can claim a custom scheme, so a
     * production build takes the DigiLocker code from its verified App Link only.
     */
    fun parse(link: String?, allowCustomSchemeDigiLocker: Boolean = false): ProDeepLink? {
        val raw = link?.trim()?.takeIf { it.isNotEmpty() } ?: return null
        DigiLockerReturnLink.parse(raw, allowCustomSchemeDigiLocker)?.let { return ProDeepLink.DigiLocker(it) }
        val uri = runCatching { URI(raw) }.getOrNull() ?: return null
        if (!uri.scheme.equals(SCHEME, ignoreCase = true)) return null
        val segments = listOfNotNull(uri.host) + uri.path.orEmpty().split('/').filter { it.isNotEmpty() }
        return route(segments)
    }

    private fun route(segments: List<String>): ProDeepLink? = when {
        segments == listOf("home") -> ProDeepLink.Home
        segments == listOf("offers") -> ProDeepLink.Offers
        segments.size == 2 && segments[0] == "offers" -> segments[1].takeIf(ID::matches)?.let(ProDeepLink::Offer)
        segments.size == 2 && segments[0] == "jobs" -> segments[1].takeIf(ID::matches)?.let(ProDeepLink::Job)
        segments.size == 3 && segments[0] == "jobs" && segments[2] == "chat" -> segments[1].takeIf(ID::matches)?.let(ProDeepLink::JobChat)
        segments == listOf("onboarding") -> ProDeepLink.Onboarding
        segments == listOf("onboarding", "police-certificate") -> ProDeepLink.PoliceCertificate
        segments == listOf("account") -> ProDeepLink.Account
        segments == listOf("earnings") -> ProDeepLink.Earnings
        else -> null
    }

    /**
     * A tapped push. notification-service sends `type`, the registry's data
     * keys and `deep_link` (doorstep_push.go); the system-rendered tap and
     * NotificationPresenter both put them on the launch intent. The deep link
     * wins; the type and its ids are the fallback when it is missing or
     * unreadable. Only doorstep.pro.* types are honoured.
     */
    fun fromPush(data: Map<String, String?>): ProDeepLink? {
        val type = data["type"]?.takeIf { it.startsWith(ProPushTypes.PREFIX) } ?: return null
        parse(data["deep_link"])?.takeIf { it !is ProDeepLink.DigiLocker }?.let { return it }
        val offerId = data["offer_id"]?.takeIf(ID::matches)
        val bookingId = (data["booking_id"] ?: data["entity_id"])?.takeIf(ID::matches)
        return when (type) {
            ProPushTypes.OFFER_NEW -> offerId?.let(ProDeepLink::Offer) ?: ProDeepLink.Offers
            ProPushTypes.OFFER_EXPIRED -> ProDeepLink.Offers
            ProPushTypes.MESSAGE_NEW -> bookingId?.let(ProDeepLink::JobChat)
            in ProPushTypes.JOB_TYPES -> bookingId?.let(ProDeepLink::Job)
            ProPushTypes.APPLICATION_APPROVED, ProPushTypes.ACCOUNT_REINSTATED -> ProDeepLink.Home
            ProPushTypes.APPLICATION_REJECTED, ProPushTypes.DOCUMENT_REVIEWED -> ProDeepLink.Onboarding
            ProPushTypes.ACCOUNT_SUSPENDED -> ProDeepLink.Account
            ProPushTypes.BACKGROUND_CHECK_EXPIRING -> ProDeepLink.PoliceCertificate
            ProPushTypes.SETTLEMENT_COMPUTED -> ProDeepLink.Earnings
            else -> null
        }
    }
}

/** The professional push types (asyncapi `x-push-types.doorstep_pro`), verbatim. */
object ProPushTypes {
    const val PREFIX = "doorstep.pro."
    const val OFFER_NEW = "doorstep.pro.offer.new"
    const val OFFER_EXPIRED = "doorstep.pro.offer.expired"
    const val JOB_CANCELLED = "doorstep.pro.job.cancelled"
    const val JOB_RESCHEDULED = "doorstep.pro.job.rescheduled"
    const val JOB_REMINDER = "doorstep.pro.job.reminder"
    const val EXTRAS_APPROVED = "doorstep.pro.extras.approved"
    const val EXTRAS_DECLINED = "doorstep.pro.extras.declined"
    const val EXTRAS_PAID = "doorstep.pro.extras.paid"
    const val MESSAGE_NEW = "doorstep.pro.message.new"
    const val APPLICATION_APPROVED = "doorstep.pro.application.approved"
    const val APPLICATION_REJECTED = "doorstep.pro.application.rejected"
    const val ACCOUNT_SUSPENDED = "doorstep.pro.account.suspended"
    const val ACCOUNT_REINSTATED = "doorstep.pro.account.reinstated"
    const val DOCUMENT_REVIEWED = "doorstep.pro.document.reviewed"
    const val BACKGROUND_CHECK_EXPIRING = "doorstep.pro.background_check.expiring"
    const val RATING_RECEIVED = "doorstep.pro.rating.received"
    const val SETTLEMENT_COMPUTED = "doorstep.pro.settlement.computed"

    val JOB_TYPES: Set<String> = setOf(
        JOB_CANCELLED, JOB_RESCHEDULED, JOB_REMINDER, EXTRAS_APPROVED, EXTRAS_DECLINED, EXTRAS_PAID, RATING_RECEIVED,
    )

    /** All 17, for the channel test. */
    val ALL: Set<String> = setOf(
        OFFER_NEW, OFFER_EXPIRED, MESSAGE_NEW, APPLICATION_APPROVED, APPLICATION_REJECTED, ACCOUNT_SUSPENDED,
        ACCOUNT_REINSTATED, DOCUMENT_REVIEWED, BACKGROUND_CHECK_EXPIRING, SETTLEMENT_COMPUTED,
    ) + JOB_TYPES

    /** The push data keys the Activity copies off a launch intent (registry data keys + transport keys). */
    val INTENT_KEYS: List<String> = listOf("type", "deep_link", "entity_id", "offer_id", "booking_id")
}

/** What came back on the DigiLocker return link. */
data class DigiLockerReturn(
    val code: String?,
    val state: String?,
    val error: String?,
    val errorDescription: String?,
)

/**
 * The DigiLocker return (doorstep-service config.DigiLockerRedirectURI):
 * DOORSTEP_PRO_APP_LINK_URL when set, else `<public base>/doorstep-pro/digilocker`.
 * The app claims that path as an App Link per flavour; in dev it also claims
 * `doorstep-pro://digilocker`, which doorstep-service cannot redirect to until
 * its URL check allows a custom scheme outside production (backend gap, the
 * same as Feast Rider's).
 */
object DigiLockerReturnLink {
    const val APP_LINK_PATH = "/doorstep-pro/digilocker"
    const val CUSTOM_HOST = "digilocker"

    fun parse(link: String?, allowCustomScheme: Boolean): DigiLockerReturn? {
        val uri = runCatching { URI(link?.trim().orEmpty()) }.getOrNull() ?: return null
        val scheme = uri.scheme?.lowercase() ?: return null
        val path = uri.path.orEmpty().trimEnd('/')
        val matches = when (scheme) {
            ProDeepLinks.SCHEME -> allowCustomScheme && uri.host.equals(CUSTOM_HOST, ignoreCase = true) && path.isEmpty()
            "http", "https" -> !uri.host.isNullOrEmpty() && path == APP_LINK_PATH
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
interface DigiLockerStateStore {
    fun save(state: String)
    fun load(): String?
    fun clear()
}

/** What the app does with a return link, before any network call. */
sealed interface ReturnCheck {
    /** Post `{code, state}` to `/pro/digilocker/callback`. */
    data class Complete(val state: String, val code: String) : ReturnCheck

    /** No verification was started on this device (or it was already finished). */
    data object NothingPending : ReturnCheck

    /** The link's state is not the one this device was given: never posted. */
    data object StateMismatch : ReturnCheck

    /** DigiLocker came back with `error` (the professional cancelled, or consent failed). */
    data class Declined(val error: String) : ReturnCheck

    data object MissingCode : ReturnCheck
}

/** The state echo: only a link carrying the state this device saved has its one-time code posted. */
object DigiLockerReturnPolicy {
    fun check(link: DigiLockerReturn, pendingState: String?): ReturnCheck {
        if (pendingState.isNullOrEmpty()) return ReturnCheck.NothingPending
        if (link.state.isNullOrEmpty() || link.state != pendingState) return ReturnCheck.StateMismatch
        link.error?.let { return ReturnCheck.Declined(it) }
        val code = link.code?.takeIf { it.isNotBlank() } ?: return ReturnCheck.MissingCode
        return ReturnCheck.Complete(state = pendingState, code = code)
    }
}

/** Whether this build accepts the dev custom-scheme DigiLocker return. Bound by the app from its flavour. */
data class ProLinkConfig(val allowCustomSchemeDigiLocker: Boolean)

/**
 * Links arriving at the Activity (cold start or singleTop re-delivery), held
 * until the signed-in shell consumes them. Replays the last one, so a link that
 * arrives before the professional's screens exist is not lost.
 */
@Singleton
class ProDeepLinkBus @Inject constructor() {
    private val links = MutableSharedFlow<ProDeepLink>(replay = 1, onBufferOverflow = BufferOverflow.DROP_OLDEST)

    val incoming: SharedFlow<ProDeepLink> = links.asSharedFlow()

    fun publish(link: ProDeepLink) {
        links.tryEmit(link)
    }

    /** Called once a link has been acted on, so a recomposition does not act on it twice. */
    @OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
    fun consumed() {
        links.resetReplayCache()
    }
}
