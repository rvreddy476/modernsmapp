package com.us.android.core.notifications

import com.google.common.truth.Truth.assertThat
import org.junit.Test

class UnifiedPartnerChannelsTest {
    @Test fun adminReviewAlertsUseRegisteredWorkerChannels() {
        mapOf(
            "food_delivery_account_updated" to NotificationChannelSpec.FOOD_ORDERS,
            "food_delivery_document_reviewed" to NotificationChannelSpec.FOOD_ORDERS,
            "captain.review.updated" to NotificationChannelSpec.CAPTAIN_ACCOUNT,
            "captain.restricted" to NotificationChannelSpec.CAPTAIN_ACCOUNT,
            "captain.document.expiring" to NotificationChannelSpec.CAPTAIN_ACCOUNT,
            "captain.subscription.review_rejected" to NotificationChannelSpec.CAPTAIN_ACCOUNT,
            "doorstep.pro.change.reviewed" to NotificationChannelSpec.DOORSTEP_PRO_ACCOUNT,
            "doorstep.pro.price.reviewed" to NotificationChannelSpec.DOORSTEP_PRO_ACCOUNT,
        ).forEach { (type, channel) -> assertThat(NotificationChannelSpec.forType(type)).isEqualTo(channel) }
    }

    @Test fun businessAlertsNeverFallIntoMissingSocialChannel() {
        mapOf(
            "food_order_new" to NotificationChannelSpec.KITCHEN_NEW_ORDER,
            "food_delivery_offer" to NotificationChannelSpec.RIDER_JOB_OFFER,
            "commerce.seller.new_order" to NotificationChannelSpec.PARTNER_BUSINESS,
            "food.restaurant.approved" to NotificationChannelSpec.FOOD_ORDERS,
            "captain.vehicle.reviewed" to NotificationChannelSpec.CAPTAIN_ACCOUNT,
            "doorstep.pro.offer.new" to NotificationChannelSpec.DOORSTEP_PRO_OFFERS,
        ).forEach { (type, channel) -> assertThat(NotificationChannelSpec.forType(type)).isEqualTo(channel) }
    }
}
