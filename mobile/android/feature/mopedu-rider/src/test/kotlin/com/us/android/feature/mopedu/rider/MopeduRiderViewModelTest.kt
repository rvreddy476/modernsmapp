package com.us.android.feature.mopedu.rider

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.mobility.model.MoneyPaise
import com.us.android.core.mobility.model.PaymentMethod
import com.us.android.core.mobility.model.RidePaymentStatus
import com.us.android.core.mobility.model.RideStatus
import com.us.android.core.mobility.model.SurgeReason
import com.us.android.core.mobility.model.VehicleType
import com.us.android.core.payments.PaymentAttempt
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.payments.PaymentHandoffEvent
import com.us.android.core.payments.PaymentOutcome
import com.us.android.core.payments.toHandoffEvent
import com.us.android.core.payments.toSheetResult
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.mopedu.rider.data.MopeduResult
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class MopeduRiderViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @get:Rule
    val main = MainDispatcherRule(dispatcher)

    private val repo = FakeRiderRepository()
    private val location = FakeLocationSource()
    private val handoff = PaymentHandoff()
    private val clock = FixedClock(now = 50_000L)

    private fun vm(handle: SavedStateHandle = SavedStateHandle()) =
        MopeduRiderViewModel(repo, location, handoff, confirmingOnlyCoordinator(), clock, handle)

    private fun TestScope.quoted(model: MopeduRiderViewModel): RiderUiState.QuoteSelect {
        advanceUntilIdle()
        model.onPickupQueryChanged(PICKUP.address)
        model.onDropQueryChanged(DROP.address)
        model.requestQuote()
        advanceUntilIdle()
        return model.uiState.value as RiderUiState.QuoteSelect
    }

    private fun TestScope.history(model: MopeduRiderViewModel): List<RiderUiState> {
        val seen = mutableListOf<RiderUiState>()
        backgroundScope.launch(dispatcher) { model.uiState.collect { seen += it } }
        return seen
    }

    /**
     * Drives the ride to `completed`. Only [runCurrent], never [advanceUntilIdle]:
     * a cash ride keeps polling the server for the captain's confirmation, and
     * a poll that never goes idle would spin the virtual clock forever.
     */
    private fun TestScope.completedRide(model: MopeduRiderViewModel, method: PaymentMethod, status: RidePaymentStatus): RiderUiState.TripCompleted {
        runCurrent()
        repo.receiptAnswer = MopeduResult.Success(receipt(paymentMethod = method, paymentStatus = status))
        repo.lastPaymentAnswer = MopeduResult.Success(payment(method, status))
        repo.activeRide = booking(status = RideStatus.COMPLETED, paymentMethod = method)
        model.checkActiveRide()
        runCurrent()
        return model.uiState.value as RiderUiState.TripCompleted
    }

    /** Books the quoted ride. [runCurrent] only: the ride poll starts here and never goes idle. */
    private fun TestScope.booked(model: MopeduRiderViewModel) {
        model.confirmBooking()
        runCurrent()
    }

    // ── The quote ───────────────────────────────────────────────────────

    @Test
    fun `initial state asks where to, and a quote lists the server's options`() = runTest(dispatcher) {
        val model = vm()
        advanceUntilIdle()
        assertThat(model.uiState.value).isInstanceOf(RiderUiState.LocationSelect::class.java)

        val state = quoted(model)
        assertThat(state.quote.options).hasSize(2)
        assertThat(state.selectedOption.vehicleType).isEqualTo(VehicleType.BIKE)
        assertThat(state.paymentMethod).isEqualTo(PaymentMethod.CASH)
        assertThat(repo.estimateCalls.single().second).isNull()
    }

    @Test
    fun `use my location fills the pickup from a one-shot fix, behind the rationale when not granted`() = runTest(dispatcher) {
        location.permission = false
        val model = vm()
        advanceUntilIdle()

        model.useCurrentLocation()
        val explaining = model.uiState.value as RiderUiState.LocationSelect
        assertThat(explaining.locationStep).isEqualTo(com.us.android.feature.mopedu.rider.location.LocationStep.ExplainingPermission)
        assertThat(explaining.pickup).isNull()

        model.onLocationRationaleAccepted()
        model.onLocationPermissionResult(granted = true, canAskAgain = true)
        advanceUntilIdle()

        val located = model.uiState.value as RiderUiState.LocationSelect
        assertThat(located.pickup).isEqualTo(PICKUP)
        assertThat(located.pickupQuery).isEqualTo(PICKUP.address)
    }

    @Test
    fun `a surge chip shows only for a named reason, never for reason none`() = runTest(dispatcher) {
        repo.quoteAnswer = {
            MopeduResult.Success(
                quote(
                    options = listOf(
                        quoteOption(VehicleType.BIKE, surgeReason = SurgeReason.NONE, surgeBasisPoints = 2500),
                        quoteOption(VehicleType.AUTO, surgeReason = SurgeReason.PEAK_HOURS, surgeBasisPoints = 2500),
                    ),
                ),
            )
        }
        val model = vm()
        val state = quoted(model)

        val bike = state.quote.options.first { it.vehicleType == VehicleType.BIKE }
        val auto = state.quote.options.first { it.vehicleType == VehicleType.AUTO }
        // Mutation guard: a surge multiplier with reason `none` must not render the chip.
        assertThat(bike.showsSurgeChip).isFalse()
        assertThat(bike.surgeReason.chipLabel).isNull()
        assertThat(auto.showsSurgeChip).isTrue()
        assertThat(auto.surgeReason.chipLabel).isEqualTo("Peak hours")
    }

    @Test
    fun `applying a coupon validates it, then re-requests the estimate with the code`() = runTest(dispatcher) {
        repo.quoteAnswer = { code ->
            MopeduResult.Success(
                quote(
                    options = listOf(quoteOption(discountPaise = if (code == null) 0 else 650L, couponCode = code)),
                    couponCode = code,
                ),
            )
        }
        val model = vm()
        quoted(model)

        model.onCouponInputChanged("save10")
        model.applyCoupon()
        advanceUntilIdle()

        val state = model.uiState.value as RiderUiState.QuoteSelect
        assertThat(repo.couponCalls).containsExactly("save10")
        assertThat(repo.estimateCalls.map { it.second }).containsExactly(null, "SAVE10").inOrder()
        assertThat(state.coupon).isInstanceOf(CouponStatus.Applied::class.java)
        assertThat(state.quote.couponCode).isEqualTo("SAVE10")
        assertThat(state.selectedOption.hasDiscount).isTrue()
        assertThat(state.selectedOption.discount).isEqualTo(MoneyPaise(650L))

        model.removeCoupon()
        advanceUntilIdle()
        val plain = model.uiState.value as RiderUiState.QuoteSelect
        assertThat(plain.coupon).isEqualTo(CouponStatus.None)
        assertThat(plain.selectedOption.hasDiscount).isFalse()
        assertThat(repo.estimateCalls.last().second).isNull()
    }

    @Test
    fun `a rejected coupon shows the server's message and leaves the quote alone`() = runTest(dispatcher) {
        repo.couponAnswer = refused("COUPON_EXPIRED", "This coupon has expired.")
        val model = vm()
        val before = quoted(model)

        model.onCouponInputChanged("OLD")
        model.applyCoupon()
        advanceUntilIdle()

        val state = model.uiState.value as RiderUiState.QuoteSelect
        assertThat(state.coupon).isEqualTo(CouponStatus.Rejected("This coupon has expired."))
        assertThat(state.quote).isEqualTo(before.quote)
        assertThat(repo.estimateCalls).hasSize(1)
    }

    @Test
    fun `an outstanding cancellation fee in the quote is surfaced on the option`() = runTest(dispatcher) {
        repo.quoteAnswer = { MopeduResult.Success(quote(options = listOf(quoteOption(outstandingPaise = 2000L)))) }
        val model = vm()
        val state = quoted(model)
        assertThat(state.selectedOption.includesOutstanding).isTrue()
        assertThat(state.selectedOption.outstanding).isEqualTo(MoneyPaise(2000L))
    }

    // ── Booking ─────────────────────────────────────────────────────────

    @Test
    fun `booking sends the chosen vehicle and payment method, and moves to searching`() = runTest(dispatcher) {
        val model = vm()
        val state = quoted(model)
        model.selectVehicleOption(state.quote.options.first { it.vehicleType == VehicleType.AUTO })
        model.selectPaymentMethod(PaymentMethod.UPI)

        booked(model)

        val (key, vehicle, method) = repo.bookings.single()
        assertThat(key).isEqualTo("key-1")
        assertThat(vehicle).isEqualTo(VehicleType.AUTO)
        assertThat(method).isEqualTo(PaymentMethod.UPI)
        assertThat(model.uiState.value).isInstanceOf(RiderUiState.SearchingCaptain::class.java)
        model.resetToNewBooking()
    }

    @Test
    fun `an expired quote is not booked`() = runTest(dispatcher) {
        repo.quoteAnswer = { MopeduResult.Success(quote(expiresAtEpochMs = 10_000L)) }
        val model = vm()
        quoted(model)
        clock.now = 20_000L

        booked(model)

        assertThat(repo.bookings).isEmpty()
        assertThat((model.uiState.value as RiderUiState.QuoteSelect).error).contains("expired")
    }

    @Test
    fun `polling follows the ride through assigned, arrived with the OTP, and in progress`() = runTest(dispatcher) {
        val model = vm()
        quoted(model)
        booked(model)

        repo.activeRide = booking(status = RideStatus.PARTNER_ASSIGNED)
        advanceTimeBy(3_000)
        runCurrent()
        assertThat(model.uiState.value).isInstanceOf(RiderUiState.CaptainAssigned::class.java)

        repo.activeRide = booking(status = RideStatus.ARRIVED)
        advanceTimeBy(3_000)
        runCurrent()
        val arrived = model.uiState.value as RiderUiState.ArrivedAtPickup
        assertThat(arrived.otp).isEqualTo("4321")

        repo.activeRide = booking(status = RideStatus.IN_PROGRESS)
        advanceTimeBy(3_000)
        runCurrent()
        assertThat(model.uiState.value).isInstanceOf(RiderUiState.TripInProgress::class.java)
        model.resetToNewBooking()
    }

    // ── Cancelling ──────────────────────────────────────────────────────

    @Test
    fun `the cancel prompt states the fee rule - free inside the window, the fee after it`() = runTest(dispatcher) {
        val model = vm()
        quoted(model)
        booked(model)

        clock.now = 40_000L // free until 100_000
        model.requestCancel()
        val free = (model.uiState.value as RiderUiState.SearchingCaptain).cancel
        assertThat(free).isEqualTo(CancelPrompt(fee = MoneyPaise.ZERO, freeSecondsLeft = 60L))
        model.dismissCancel()
        assertThat((model.uiState.value as RiderUiState.SearchingCaptain).cancel).isNull()

        clock.now = 130_000L // after the window: the fee applies
        model.requestCancel()
        val charged = (model.uiState.value as RiderUiState.SearchingCaptain).cancel
        assertThat(charged).isEqualTo(CancelPrompt(fee = MoneyPaise(2000L), freeSecondsLeft = null))
        assertThat(charged!!.isFree).isFalse()

        model.confirmCancel()
        runCurrent()
        assertThat(repo.cancelCalls).containsExactly("ride-456")
        val cancelled = model.uiState.value as RiderUiState.Cancelled
        assertThat(cancelled.fee).isEqualTo(MoneyPaise(2000L))
        assertThat(cancelled.byCustomer).isTrue()
    }

    @Test
    fun `confirming without a prompt does nothing, and a refused cancel keeps the ride`() = runTest(dispatcher) {
        val model = vm()
        quoted(model)
        booked(model)

        model.confirmCancel()
        runCurrent()
        assertThat(repo.cancelCalls).isEmpty()

        repo.cancelAnswer = refused("RIDE_NOT_CANCELLABLE", "Your captain has already started the trip.", status = 409)
        model.requestCancel()
        model.confirmCancel()
        runCurrent()
        val state = model.uiState.value as RiderUiState.SearchingCaptain
        assertThat(state.cancel).isNull()
        assertThat(state.error).isEqualTo("Your captain has already started the trip.")
        model.resetToNewBooking()
    }

    // ── After the trip: the money ───────────────────────────────────────

    @Test
    fun `a completed cash ride waits for the captain's confirmation, from the server only`() = runTest(dispatcher) {
        val model = vm()
        val completed = completedRide(model, PaymentMethod.CASH, RidePaymentStatus.CASH_PENDING)
        assertThat(completed.payment).isEqualTo(PaymentPhase.CashPending(MoneyPaise(6500L)))

        repo.lastPaymentAnswer = MopeduResult.Success(payment(PaymentMethod.CASH, RidePaymentStatus.CASH_CONFIRMED))
        advanceTimeBy(5_000)
        runCurrent()
        assertThat((model.uiState.value as RiderUiState.TripCompleted).payment).isEqualTo(PaymentPhase.CashConfirmed)
        model.resetToNewBooking()
    }

    @Test
    fun `a completed UPI ride offers pay now, which creates the intent and asks for the sheet`() = runTest(dispatcher) {
        val model = vm()
        val completed = completedRide(model, PaymentMethod.UPI, RidePaymentStatus.CONFIRMING)
        assertThat(completed.payment).isEqualTo(PaymentPhase.ReadyToPay(PaymentMethod.UPI, MoneyPaise(6500L)))

        model.payNow()
        advanceUntilIdle()

        assertThat(repo.intentCalls).containsExactly("ride-456" to PaymentMethod.UPI)
        val opening = (model.uiState.value as RiderUiState.TripCompleted).payment as PaymentPhase.OpeningSheet
        assertThat(opening.request.attempt).isEqualTo(PaymentAttempt("mopedu", "ride-456", "key-1"))
        assertThat(opening.request.session.applicationId).isEqualTo("mopedu")
        assertThat(opening.request.session.providerOrderId).isEqualTo("order_rzp_1")
        assertThat(model.activeAttempt()).isEqualTo(opening.request.attempt)
    }

    @Test
    fun `the SDK reporting success never shows paid while the server says confirming`() = runTest(dispatcher) {
        val model = vm()
        val seen = history(model)
        completedRide(model, PaymentMethod.UPI, RidePaymentStatus.CONFIRMING)
        model.payNow()
        advanceUntilIdle()
        val attempt = model.activeAttempt()!!

        // Exactly what Razorpay's onPaymentSuccess becomes on the bus.
        handoff.publish(PaymentOutcome.Succeeded("pay_123").toSheetResult(attempt).toHandoffEvent())
        runCurrent()
        assertThat((model.uiState.value as RiderUiState.TripCompleted).payment).isInstanceOf(PaymentPhase.Confirming::class.java)

        advanceUntilIdle() // the whole 180 s poll
        runCurrent()

        assertThat(seen.filterIsInstance<RiderUiState.TripCompleted>().map { it.payment }.filter { it == PaymentPhase.Paid }).isEmpty()
        assertThat((model.uiState.value as RiderUiState.TripCompleted).payment).isEqualTo(PaymentPhase.StillConfirming)
        assertThat(repo.paymentReads).isGreaterThan(1)
    }

    @Test
    fun `paid is shown only once the status source says paid`() = runTest(dispatcher) {
        val model = vm()
        completedRide(model, PaymentMethod.CARD, RidePaymentStatus.CONFIRMING)
        model.payNow()
        advanceUntilIdle()
        val attempt = model.activeAttempt()!!
        repo.paymentAnswers += MopeduResult.Success(payment(PaymentMethod.CARD, RidePaymentStatus.CONFIRMING))
        repo.paymentAnswers += MopeduResult.Success(payment(PaymentMethod.CARD, RidePaymentStatus.CONFIRMING))
        repo.paymentAnswers += MopeduResult.Success(payment(PaymentMethod.CARD, RidePaymentStatus.PAID))

        handoff.publish(PaymentHandoffEvent.SheetClosed(attempt))
        advanceTimeBy(3_500) // reads at 1 s and 3 s: both confirming
        runCurrent()
        assertThat((model.uiState.value as RiderUiState.TripCompleted).payment).isInstanceOf(PaymentPhase.Confirming::class.java)

        advanceUntilIdle()
        runCurrent()

        assertThat((model.uiState.value as RiderUiState.TripCompleted).payment).isEqualTo(PaymentPhase.Paid)
        assertThat(model.activeAttempt()).isNull()
    }

    @Test
    fun `a sheet that never opened is a failure that offers retry and cash`() = runTest(dispatcher) {
        val model = vm()
        completedRide(model, PaymentMethod.UPI, RidePaymentStatus.CONFIRMING)
        model.payNow()
        advanceUntilIdle()
        val attempt = model.activeAttempt()!!

        handoff.publish(PaymentHandoffEvent.Unavailable(attempt, "no network"))
        runCurrent()

        val failed = (model.uiState.value as RiderUiState.TripCompleted).payment as PaymentPhase.Failed
        assertThat(failed.method).isEqualTo(PaymentMethod.UPI)
        assertThat(failed.reason).isEqualTo("no network")
        assertThat(failed.retryable).isTrue()

        model.switchToCash()
        runCurrent() // cash starts its own poll; never advance to idle past this point
        assertThat(repo.switchToCashCalls).isEqualTo(1)
        assertThat((model.uiState.value as RiderUiState.TripCompleted).payment).isEqualTo(PaymentPhase.CashPending(MoneyPaise(6500L)))
        model.resetToNewBooking()
    }

    @Test
    fun `an ending for another attempt or another application is ignored`() = runTest(dispatcher) {
        val model = vm()
        completedRide(model, PaymentMethod.UPI, RidePaymentStatus.CONFIRMING)
        model.payNow()
        advanceUntilIdle()
        val mine = model.activeAttempt()!!

        handoff.publish(PaymentHandoffEvent.SheetClosed(mine.copy(id = "an-older-attempt")))
        handoff.publish(PaymentHandoffEvent.SheetClosed(mine.copy(applicationId = "feast")))
        handoff.publish(PaymentHandoffEvent.SheetClosed(mine.copy(applicationId = "dating")))
        runCurrent()

        assertThat((model.uiState.value as RiderUiState.TripCompleted).payment).isInstanceOf(PaymentPhase.OpeningSheet::class.java)
        assertThat(repo.paymentReads).isEqualTo(1) // only the read at completion
    }

    @Test
    fun `after process death with a sheet requested, the server is asked and no sheet reopens`() = runTest(dispatcher) {
        val handle = SavedStateHandle()
        val first = vm(handle)
        completedRide(first, PaymentMethod.UPI, RidePaymentStatus.CONFIRMING)
        first.payNow()
        advanceUntilIdle()
        assertThat(first.activeAttempt()).isNotNull()

        repo.receiptAnswer = MopeduResult.Success(receipt(PaymentMethod.UPI, RidePaymentStatus.PAID))
        repo.lastPaymentAnswer = MopeduResult.Success(payment(PaymentMethod.UPI, RidePaymentStatus.PAID))
        val second = vm(handle)
        advanceUntilIdle()
        runCurrent()

        val state = second.uiState.value as RiderUiState.TripCompleted
        assertThat(state.payment).isEqualTo(PaymentPhase.Paid)
        assertThat(repo.intentCalls).hasSize(1) // no second intent, no second sheet
        assertThat(second.activeAttempt()).isNull()
    }

    @Test
    fun `a refund outranks paid`() = runTest(dispatcher) {
        val model = vm()
        val completed = completedRide(model, PaymentMethod.CARD, RidePaymentStatus.PARTIALLY_REFUNDED)
        assertThat(completed.payment).isEqualTo(PaymentPhase.Refunding)
    }
}
