package com.us.android.core.mobility.model

import com.google.common.truth.Truth.assertThat
import org.junit.Test

class MobilityModelsTest {

    @Test
    fun moneyPaise_formattedINR_roundsCorrectly() {
        val whole = MoneyPaise(15000L) // ₹150
        assertThat(whole.formattedINR).isEqualTo("₹150")

        val withPaise = MoneyPaise(15050L) // ₹150.50
        assertThat(withPaise.formattedINR).isEqualTo("₹150.50")
    }

    @Test
    fun moneyPaise_arithmetic() {
        val a = MoneyPaise(3000L)
        val b = MoneyPaise(1200L)
        assertThat((a + b).paise).isEqualTo(4200L)
        assertThat((a - b).paise).isEqualTo(1800L)
    }

    @Test
    fun rideStatus_terminalCheck() {
        assertThat(RideStatus.COMPLETED.isTerminal).isTrue()
        assertThat(RideStatus.CANCELLED_BY_CUSTOMER.isTerminal).isTrue()
        assertThat(RideStatus.IN_PROGRESS.isTerminal).isFalse()
        assertThat(RideStatus.PARTNER_ASSIGNED.isActive).isTrue()
    }

    @Test
    fun vehicleType_fromCodeFallback() {
        assertThat(VehicleType.fromCode("auto")).isEqualTo(VehicleType.AUTO)
        assertThat(VehicleType.fromCode("unknown")).isEqualTo(VehicleType.BIKE)
    }

    @Test
    fun partnerProfile_and_subscription_models() {
        val profile = PartnerProfile(
            id = "partner-1",
            partnerType = "individual_driver",
            fullName = "Rahul Sharma",
            phone = "+919876543210",
            status = "approved",
            kycStatus = "approved",
        )
        assertThat(profile.fullName).isEqualTo("Rahul Sharma")
        assertThat(profile.kycStatus).isEqualTo("approved")

        val sub = PartnerSubscription(
            id = "sub-1",
            partnerId = "partner-1",
            planId = "plan-1",
            planCode = "trial_7d",
            planName = "7-Day Free Trial",
            status = "trial",
            leadsUsed = 2,
            dailyLeadCap = 10,
            startsAt = "2026-08-28T00:00:00Z",
            expiresAt = "2026-09-04T00:00:00Z",
        )
        assertThat(sub.status).isEqualTo("trial")
        assertThat(sub.leadsUsed).isEqualTo(2)
    }
}
