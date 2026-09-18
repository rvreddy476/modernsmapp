package com.us.android.feature.mopedu.captain.navigation

import kotlinx.coroutines.channels.BufferOverflow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.asSharedFlow
import javax.inject.Inject
import javax.inject.Singleton

/** Where a tapped push sends the captain (2026-09-18). */
sealed interface CaptainDeepLink {
    /** The plan is expiring, expired, renewed or its payment failed: the plans screen. */
    data object Plans : CaptainDeepLink

    /** Onboarding was approved or sent to review: the onboarding status. */
    data object OnboardingStatus : CaptainDeepLink
}

object CaptainDeepLinks {
    const val TYPE_SUBSCRIPTION_EXPIRING = "captain.subscription.expiring"
    const val TYPE_SUBSCRIPTION_EXPIRED = "captain.subscription.expired"
    const val TYPE_SUBSCRIPTION_RENEWED = "captain.subscription.renewed"
    const val TYPE_SUBSCRIPTION_PAYMENT_FAILED = "captain.subscription.payment_failed"
    const val TYPE_APPROVED = "captain.approved"
    const val TYPE_UNDER_REVIEW = "captain.under_review"

    /**
     * The push's `type` extra (the presenter's content intent and FCM's own
     * launch extras carry the same key). An offer, or a type this build does
     * not know, is not a link: the captain simply lands where they were.
     */
    fun fromPush(type: String?): CaptainDeepLink? = when (type) {
        TYPE_SUBSCRIPTION_EXPIRING, TYPE_SUBSCRIPTION_EXPIRED, TYPE_SUBSCRIPTION_RENEWED, TYPE_SUBSCRIPTION_PAYMENT_FAILED ->
            CaptainDeepLink.Plans
        TYPE_APPROVED, TYPE_UNDER_REVIEW -> CaptainDeepLink.OnboardingStatus
        else -> null
    }
}

/**
 * Links arriving at the Activity (cold start or singleTop re-delivery), held
 * until the signed-in console consumes them. Replays the last one, so a link
 * that arrives before the console exists is not lost; the console consumes it
 * once.
 */
@Singleton
class CaptainDeepLinkBus @Inject constructor() {
    private val links = MutableSharedFlow<CaptainDeepLink>(replay = 1, onBufferOverflow = BufferOverflow.DROP_OLDEST)

    val incoming: SharedFlow<CaptainDeepLink> = links.asSharedFlow()

    fun publish(link: CaptainDeepLink) {
        links.tryEmit(link)
    }

    /** Forgets the replayed link once it has been acted on, so a recreated console does not act on it twice. */
    @OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
    fun consume() {
        links.resetReplayCache()
    }
}
