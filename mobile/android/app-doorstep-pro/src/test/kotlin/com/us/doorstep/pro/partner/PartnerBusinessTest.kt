package com.us.doorstep.pro.partner

import com.google.common.truth.Truth.assertThat
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import org.junit.Test

class PartnerBusinessTest {
    @Test fun onlyRequestedWorkerCategoriesExist() {
        assertThat(PartnerBusiness.entries.map { it.title }).containsExactly(
            "Moped", "Home Services", "Commerce Delivery", "Food Delivery").inOrder()
        assertThat(PartnerBusiness.entries.mapNotNull { it.endpoint }).containsExactly(
            "v1/doorstep/pro/me", "v1/food/delivery/profile", "v1/rider/partners/me", "v1/commerce/delivery/profile")
    }
    @Test fun fishSelectionsAreDiscardedAndNotReassigned() {
        assertThat(selectedBusinesses(setOf("FISH_DELIVERY", "FISH"))).isEmpty()
        assertThat(PartnerBusiness.entries.none { it.name.contains("FISH") }).isTrue()
    }
    @Test fun upgradePreservesWorkerSelectionsAndDropsMerchantSelections() {
        assertThat(selectedBusinesses(setOf("HOME", "FOOD", "SELLING", "DELIVERY", "RIDE", "unknown")))
            .containsExactly(PartnerBusiness.HOME, PartnerBusiness.FOOD_DELIVERY, PartnerBusiness.MOPED)
        assertThat(selectedBusinesses(setOf("FOOD_DELIVERY", "COMMERCE_DELIVERY"))).containsExactly(PartnerBusiness.FOOD_DELIVERY, PartnerBusiness.COMMERCE_DELIVERY)
    }
    @Test fun absentProfilesAreNotErrors() {
        mapOf(
            PartnerBusiness.HOME to """{"code":"DOORSTEP_PRO_NOT_FOUND"}""",
            PartnerBusiness.MOPED to """{"code":"NOT_FOUND","message":"no partner profile"}""",
            PartnerBusiness.FOOD_DELIVERY to """{"code":"FOOD_DELIVERY_PROFILE_NOT_FOUND"}""",
            PartnerBusiness.COMMERCE_DELIVERY to """{"code":"COMMERCE_DELIVERY_PROFILE_NOT_FOUND"}""",
        ).forEach { (business, raw) ->
            val result = businessFailureSummary(business, 404, Json.parseToJsonElement(raw).jsonObject)
            assertThat(result.status).isEqualTo(BusinessStatus.NEW)
            assertThat(result.registered).isFalse()
        }
    }
    @Test fun routingAndServerErrorsNeverPretendProfileIsAbsent() {
        for (business in PartnerBusiness.entries.filter { it.available }) {
            for (status in listOf(401, 403, 404, 500)) {
                assertThat(businessFailureSummary(business, status,
                    Json.parseToJsonElement("""{"code":"NOT_FOUND","message":"not found"}""").jsonObject).status)
                    .isEqualTo(if (status == 404) BusinessStatus.UNAVAILABLE else BusinessStatus.UNKNOWN)
            }
        }
    }
    @Test fun closingMopedDoesNotEraseAnExistingRegistrationOrRetainApprovedBadge() {
        val previous = BusinessSummary(PartnerBusiness.MOPED, BusinessStatus.APPROVED, true, "Test Partner")
        val unavailable = BusinessSummary(PartnerBusiness.MOPED, BusinessStatus.UNAVAILABLE)
        val result = mergeBusinessRefresh(previous, unavailable)
        assertThat(result.registered).isTrue()
        assertThat(result.name).isEqualTo("Test Partner")
        assertThat(result.status).isEqualTo(BusinessStatus.UNAVAILABLE)
        assertThat(result.canOpen).isFalse()
        val restored = mergeBusinessRefresh(result, previous)
        assertThat(restored.status).isEqualTo(BusinessStatus.APPROVED)
        assertThat(restored.canOpen).isTrue()
    }
    @Test fun anExplicitAbsentProfileDoesNotKeepAnOldRegistration() {
        val previous = BusinessSummary(PartnerBusiness.MOPED, BusinessStatus.APPROVED, true)
        val absent = BusinessSummary(PartnerBusiness.MOPED, BusinessStatus.NEW)
        assertThat(mergeBusinessRefresh(previous, absent).registered).isFalse()
        assertThat(absent.canOpen).isTrue()
    }
    @Test fun approvedHomeUsesCurrentStatusAndRealDisplayName() {
        val result = businessSummary(PartnerBusiness.HOME, Json.parseToJsonElement("""{"id":"home-1","status":"approved","display_name":"Test Partner","application":{"submitted_at":"2026-10-10T00:00:00Z"}}"""))
        assertThat(result.name).isEqualTo("Test Partner")
        assertThat(result.status).isEqualTo(BusinessStatus.APPROVED)
    }
    @Test fun compactGridAdaptsToSmallScreensAndLargeText() {
        assertThat(businessColumnCount(320f, 1f)).isEqualTo(1)
        assertThat(businessColumnCount(360f, 1f)).isEqualTo(2)
        assertThat(businessColumnCount(420f, 1.5f)).isEqualTo(1)
    }
    @Test fun businessesRetainIndependentApproval() {
        val approved = businessSummary(PartnerBusiness.HOME, Json.parseToJsonElement("""{"id":"home-1","status":"approved"}"""))
        val pending = businessSummary(PartnerBusiness.FOOD_DELIVERY, Json.parseToJsonElement("""{"id":"delivery-1","status":"PENDING_REVIEW"}"""))
        assertThat(approved.registered).isTrue()
        assertThat(approved.status).isEqualTo(BusinessStatus.APPROVED)
        assertThat(pending.status).isEqualTo(BusinessStatus.REVIEW)
    }
    @Test fun malformedProfilesNeverBecomeApproved() {
        for (raw in listOf("{}", """{"id":"1","status":"surprise"}""", "null")) {
            assertThat(businessSummary(PartnerBusiness.MOPED, Json.parseToJsonElement(raw)).status).isEqualTo(BusinessStatus.UNKNOWN)
        }
    }
    @Test fun customerAndMerchantPushesCannotOpenAWorkspace() {
        for (type in listOf("doorstep.booking.confirmed", "comment", "food_order_status", "food_order_new",
            "food.restaurant.approved", "seller.new_order", "commerce.seller.new_order")) {
            assertThat(PartnerBusiness.forNotification(type)).isNull()
        }
    }
    @Test fun workerPushesOpenTheirOwnWorkspace() {
        mapOf("doorstep.pro.offer.new" to PartnerBusiness.HOME, "captain.offer" to PartnerBusiness.MOPED,
            "food_delivery_offer" to PartnerBusiness.FOOD_DELIVERY).forEach { (type, role) ->
            assertThat(PartnerBusiness.forNotification(type)).isEqualTo(role)
        }
    }
}
