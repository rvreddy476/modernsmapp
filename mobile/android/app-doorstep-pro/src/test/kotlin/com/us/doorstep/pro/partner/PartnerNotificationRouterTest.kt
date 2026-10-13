package com.us.doorstep.pro.partner

import com.google.common.truth.Truth.assertThat
import com.us.android.feature.doorsteppro.deeplink.ProDeepLink
import com.us.android.feature.doorsteppro.deeplink.ProDeepLinkBus
import com.us.android.feature.rider.deeplink.RiderDeepLink
import com.us.android.feature.rider.deeplink.RiderDeepLinkBus
import com.us.android.feature.mopedu.captain.navigation.CaptainDeepLink
import com.us.android.feature.mopedu.captain.navigation.CaptainDeepLinkBus
import org.junit.Test

class PartnerNotificationRouterTest {
    private val pro = ProDeepLinkBus()
    private val rider = RiderDeepLinkBus()
    private val captain = CaptainDeepLinkBus()
    private val commerce = CommerceWorkLinks()
    private val router = PartnerNotificationRouter(pro, rider, captain, commerce)

    @Test fun commerceTapsRouteToAuthoritativeWorkEarningsOrApplication() {
        router.open("commerce.delivery.offer", "offer-1", "/partner/commerce-delivery")
        assertThat(commerce.destination.value?.page).isEqualTo("Requests")
        assertThat(commerce.destination.value?.jobId).isNull()
        router.open("commerce.delivery.delivered", "job-1", "/partner/commerce-delivery")
        assertThat(commerce.destination.value?.jobId).isEqualTo("job-1")
        router.open("commerce.delivery.earnings", "job-1", "/partner/commerce-delivery")
        assertThat(commerce.destination.value?.page).isEqualTo("Earnings")
        router.open("commerce.delivery.application.approved", "profile-1", "/partner/commerce-delivery")
        assertThat(commerce.destination.value?.page).isEqualTo("Application")
    }

    @Test fun currentWorkOpensItsAuthoritativeCategoryJob() {
        router.openWork(PartnerBusiness.HOME, "booking-1")
        assertThat(pro.incoming.replayCache.single()).isEqualTo(ProDeepLink.Job("booking-1"))
        router.openWork(PartnerBusiness.MOPED, "ride-1")
        assertThat(captain.incoming.replayCache.single()).isEqualTo(CaptainDeepLink.Ride("ride-1"))
        router.openWork(PartnerBusiness.FOOD_DELIVERY, "assignment-1")
        assertThat(rider.incoming.replayCache.single()).isEqualTo(RiderDeepLink.CurrentJob)
    }

    @Test fun homeJobTapOpensTheActualJobNotOnlyBusinessHome() {
        assertThat(router.open("doorstep.pro.job.cancelled", "booking-1", "doorstep-pro://jobs/booking-1"))
            .isEqualTo(PartnerBusiness.HOME)
        assertThat(pro.incoming.replayCache.single()).isEqualTo(ProDeepLink.Job("booking-1"))
    }

    @Test fun foodReviewAndOfferTapsSurviveUntilWorkspaceStarts() {
        assertThat(router.open("food_delivery_document_reviewed", "doc-1", "/rider/account"))
            .isEqualTo(PartnerBusiness.FOOD_DELIVERY)
        assertThat(rider.incoming.replayCache.single()).isEqualTo(RiderDeepLink.Documents)
        rider.consumed()
        router.open("food_delivery_account_updated", "partner-1", "/rider/account")
        assertThat(rider.incoming.replayCache.single()).isEqualTo(RiderDeepLink.Account)
        rider.consumed()
        router.open("food_delivery_offer", "order-1", "/rider/offers/offer-1")
        assertThat(rider.incoming.replayCache.single()).isEqualTo(RiderDeepLink.Offer("offer-1"))
    }

    @Test fun mopedReviewsAndPaymentDecisionsOpenTheirOwnScreens() {
        for (type in listOf("captain.review.updated", "captain.restricted", "captain.document.expiring")) {
            assertThat(router.open(type, "partner-1", "/captain/account")).isEqualTo(PartnerBusiness.MOPED)
            assertThat(captain.incoming.replayCache.single()).isEqualTo(CaptainDeepLink.OnboardingStatus)
            captain.consume()
        }
        router.open("captain.subscription.review_rejected", "partner-1", "/captain/subscription")
        assertThat(captain.incoming.replayCache.single()).isEqualTo(CaptainDeepLink.Plans)
    }

    @Test fun customerAndMerchantAlertsCannotOpenWorkerWorkspaces() {
        for (type in listOf("food_order_new", "food_order_status", "commerce.order.placed", "ride.assigned", "doorstep.booking.assigned")) {
            assertThat(router.open(type, "id", "doorstep-pro://onboarding")).isNull()
        }
        assertThat(pro.incoming.replayCache).isEmpty()
        assertThat(rider.incoming.replayCache).isEmpty()
        assertThat(captain.incoming.replayCache).isEmpty()
    }
}
