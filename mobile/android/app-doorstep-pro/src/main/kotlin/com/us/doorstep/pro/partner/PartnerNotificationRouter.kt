package com.us.doorstep.pro.partner

import com.us.android.feature.doorsteppro.deeplink.ProDeepLinkBus
import com.us.android.feature.doorsteppro.deeplink.ProDeepLinks
import com.us.android.feature.rider.deeplink.RiderDeepLinkBus
import com.us.android.feature.rider.deeplink.RiderDeepLinks
import com.us.android.feature.mopedu.captain.navigation.CaptainDeepLinkBus
import com.us.android.feature.mopedu.captain.navigation.CaptainDeepLinks
import javax.inject.Inject
import com.us.android.feature.doorsteppro.deeplink.ProDeepLink
import com.us.android.feature.rider.deeplink.RiderDeepLink
import com.us.android.feature.mopedu.captain.navigation.CaptainDeepLink

/** Inbox and system-tray taps share routing and the same server-authorized screens. */
class PartnerNotificationRouter @Inject constructor(
    private val pro: ProDeepLinkBus,
    private val rider: RiderDeepLinkBus,
    private val captain: CaptainDeepLinkBus,
    private val commerce: CommerceWorkLinks = CommerceWorkLinks(),
) {
    fun openRequests(business: PartnerBusiness) {
        when (business) {
            PartnerBusiness.HOME -> pro.publish(ProDeepLink.Offers)
            PartnerBusiness.MOPED -> captain.publish(CaptainDeepLink.Work)
            PartnerBusiness.FOOD_DELIVERY -> rider.publish(RiderDeepLink.Work)
            PartnerBusiness.COMMERCE_DELIVERY -> commerce.open("Requests")
        }
    }
    fun openWork(business: PartnerBusiness, jobId: String) {
        when (business) {
            PartnerBusiness.HOME -> pro.publish(ProDeepLink.Job(jobId))
            PartnerBusiness.MOPED -> captain.publish(CaptainDeepLink.Ride(jobId))
            PartnerBusiness.FOOD_DELIVERY -> rider.publish(RiderDeepLink.CurrentJob)
            PartnerBusiness.COMMERCE_DELIVERY -> commerce.open("Requests", jobId)
        }
    }
    fun open(type: String, entityId: String, deepLink: String, offerId: String? = null): PartnerBusiness? {
        val business = PartnerBusiness.forNotification(type) ?: return null
        val data = mapOf("type" to type, "entity_id" to entityId, "deep_link" to deepLink, "offer_id" to offerId)
        when (business) {
            PartnerBusiness.HOME -> ProDeepLinks.fromPush(data)?.let(pro::publish)
            PartnerBusiness.FOOD_DELIVERY -> RiderDeepLinks.fromPush(data)?.let(rider::publish)
            PartnerBusiness.MOPED -> CaptainDeepLinks.fromPush(type, entityId)?.let(captain::publish)
            PartnerBusiness.COMMERCE_DELIVERY -> when {
                type.contains("application") || type.contains("document") || type.contains("profile") -> commerce.open("Application")
                type.contains("earning") -> commerce.open("Earnings")
                type.contains("offer") -> commerce.open("Requests")
                else -> commerce.open("Requests", entityId)
            }
        }
        return business
    }
}
