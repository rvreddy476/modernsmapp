package com.us.android.core.mobility.model

import com.google.common.truth.Truth.assertThat
import org.junit.Test

class MobilityModelsTest {

    @Test
    fun moneyPaise_formattedINR_roundsCorrectly() {
        assertThat(MoneyPaise(15000L).formattedINR).isEqualTo("₹150")
        assertThat(MoneyPaise(15050L).formattedINR).isEqualTo("₹150.50")
        assertThat(MoneyPaise(5L).formattedINR).isEqualTo("₹0.05")
        assertThat(MoneyPaise.ZERO.isZero).isTrue()
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
        assertThat(RideStatus.CANCELLED_BY_CUSTOMER.isCancelled).isTrue()
        assertThat(RideStatus.IN_PROGRESS.isTerminal).isFalse()
        assertThat(RideStatus.PARTNER_ASSIGNED.isActive).isTrue()
        assertThat(RideStatus.SCHEDULED.isActive).isFalse()
        assertThat(RideStatus.fromCode("no_such_status")).isEqualTo(RideStatus.REQUESTED)
    }

    @Test
    fun vehicleType_fromCodeFallback() {
        assertThat(VehicleType.fromCode("auto")).isEqualTo(VehicleType.AUTO)
        assertThat(VehicleType.fromCode("AUTO")).isEqualTo(VehicleType.AUTO)
        assertThat(VehicleType.fromCode("unknown")).isEqualTo(VehicleType.BIKE)
    }

    @Test
    fun paymentMethod_hasNoWallet_andOnlineIsUpiOrCard() {
        assertThat(PaymentMethod.entries.map { it.code }).containsExactly("cash", "upi", "card").inOrder()
        assertThat(PaymentMethod.fromCode("wallet")).isEqualTo(PaymentMethod.CASH)
        assertThat(PaymentMethod.CASH.isOnline).isFalse()
        assertThat(PaymentMethod.UPI.isOnline).isTrue()
        assertThat(PaymentMethod.CARD.isOnline).isTrue()
    }

    @Test
    fun surgeReason_noneNeverShowsAChip_namedReasonsDo_unknownIsGeneric() {
        assertThat(SurgeReason.fromCode("none")).isEqualTo(SurgeReason.NONE)
        assertThat(SurgeReason.fromCode(null)).isEqualTo(SurgeReason.NONE)
        assertThat(SurgeReason.fromCode("")).isEqualTo(SurgeReason.NONE)
        assertThat(SurgeReason.NONE.chipLabel).isNull()
        assertThat(SurgeReason.fromCode("peak_hours").chipLabel).isEqualTo("Peak hours")
        assertThat(SurgeReason.fromCode("high_demand").chipLabel).isEqualTo("High demand")
        assertThat(SurgeReason.fromCode("festival").chipLabel).isEqualTo("Demand pricing")

        val option = quoteOption(surgeBasisPoints = 2500, surgeReason = SurgeReason.NONE)
        // Mutation guard: a surge multiplier with reason `none` must not render the chip.
        assertThat(option.showsSurgeChip).isFalse()
        assertThat(option.copy(surgeReason = SurgeReason.PEAK_HOURS).showsSurgeChip).isTrue()
    }

    @Test
    fun quoteOption_discountAndOutstandingLines() {
        val plain = quoteOption()
        assertThat(plain.hasDiscount).isFalse()
        assertThat(plain.includesOutstanding).isFalse()

        val discounted = plain.copy(discount = MoneyPaise(1500L))
        assertThat(discounted.hasDiscount).isTrue()

        val withFee = plain.copy(breakdown = plain.breakdown.copy(outstandingPaise = 2000L))
        assertThat(withFee.includesOutstanding).isTrue()
        assertThat(withFee.outstanding).isEqualTo(MoneyPaise(2000L))
    }

    @Test
    fun cancellationRule_freeInsideTheWindow_feeAfterIt_noneWhenTheRideCarriesNoFee() {
        val booking = booking(cancellationFee = MoneyPaise(2000L), cancelFreeUntilEpochMs = 100_000L)
        assertThat(booking.cancellationFeeAt(nowEpochMs = 50_000L)).isEqualTo(MoneyPaise.ZERO)
        assertThat(booking.cancellationFeeAt(nowEpochMs = 100_000L)).isEqualTo(MoneyPaise.ZERO)
        assertThat(booking.cancellationFeeAt(nowEpochMs = 100_001L)).isEqualTo(MoneyPaise(2000L))
        assertThat(CancellationRule.freeSecondsLeft(booking, nowEpochMs = 40_000L)).isEqualTo(60L)
        assertThat(CancellationRule.freeSecondsLeft(booking, nowEpochMs = 100_000L)).isNull()

        // A fee with no window applies at once; no fee at all is always free.
        assertThat(booking(cancellationFee = MoneyPaise(500L), cancelFreeUntilEpochMs = null).cancellationFeeAt(1L))
            .isEqualTo(MoneyPaise(500L))
        assertThat(booking(cancellationFee = null, cancelFreeUntilEpochMs = null).cancellationFeeAt(1L))
            .isEqualTo(MoneyPaise.ZERO)
    }

    @Test
    fun ridePaymentStatus_settledOnlyByPaidOrConfirmedCash_unknownNeverSettles() {
        assertThat(RidePaymentStatus.fromCode("paid").isSettled).isTrue()
        assertThat(RidePaymentStatus.fromCode("cash_confirmed").isSettled).isTrue()
        assertThat(RidePaymentStatus.fromCode("cash_pending").isSettled).isFalse()
        assertThat(RidePaymentStatus.fromCode("confirming").isSettled).isFalse()
        assertThat(RidePaymentStatus.fromCode("failed").isSettled).isFalse()
        assertThat(RidePaymentStatus.fromCode("refunded").isRefund).isTrue()
        assertThat(RidePaymentStatus.fromCode("partially_refunded").isRefund).isTrue()
        val newer = RidePaymentStatus.fromCode("settled_v2")
        assertThat(newer).isEqualTo(RidePaymentStatus.UNKNOWN)
        assertThat(newer.isSettled).isFalse()
        assertThat(RidePaymentStatus.fromCode("")).isEqualTo(RidePaymentStatus.UNKNOWN)
    }

    @Test
    fun captainOffer_countdown() {
        val offer = CaptainOffer(
            id = "o-1",
            rideId = "r-1",
            pickup = GeoPoint(1.0, 2.0),
            drop = GeoPoint(3.0, 4.0),
            distanceKM = 4.2,
            estimatedEarnings = MoneyPaise(9500L),
            score = 1.0,
            expiresAtEpochMs = 30_000L,
        )
        assertThat(offer.secondsLeftAt(10_000L)).isEqualTo(20L)
        assertThat(offer.secondsLeftAt(31_000L)).isEqualTo(0L)
        assertThat(offer.isExpiredAt(29_999L)).isFalse()
        assertThat(offer.isExpiredAt(30_000L)).isTrue()
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
        assertThat(profile.rating).isEqualTo(PartnerProfile.DEFAULT_RATING)

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
        assertThat(sub.isUsable).isTrue()
        assertThat(sub.copy(status = "expired").isUsable).isFalse()
    }

    private fun quoteOption(surgeBasisPoints: Long = 0, surgeReason: SurgeReason = SurgeReason.NONE) = QuoteOption(
        vehicleType = VehicleType.BIKE,
        available = true,
        pickupETASeconds = 180,
        distanceMeters = 5000,
        durationSeconds = 600,
        totalFare = MoneyPaise(6500L),
        breakdown = QuoteBreakdown(basePaise = 3000L, distancePaise = 2500L, timePaise = 500L, taxPaise = 500L),
        surgeBasisPoints = surgeBasisPoints,
        surgeReason = surgeReason,
    )

    private fun booking(cancellationFee: MoneyPaise?, cancelFreeUntilEpochMs: Long?) = RideBooking(
        id = "ride-1",
        customerUserId = "u-1",
        partnerId = null,
        vehicleId = null,
        quoteId = "q-1",
        revision = 1,
        vehicleType = VehicleType.BIKE,
        status = RideStatus.PARTNER_ASSIGNED,
        pickup = GeoPoint(1.0, 2.0, "A"),
        drop = GeoPoint(3.0, 4.0, "B"),
        estimatedFare = MoneyPaise(6500L),
        finalFare = null,
        paymentMethod = PaymentMethod.CASH,
        otp = "1234",
        requestedAtEpochMs = 0L,
        cancellationFee = cancellationFee,
        cancelFreeUntilEpochMs = cancelFreeUntilEpochMs,
    )
}
