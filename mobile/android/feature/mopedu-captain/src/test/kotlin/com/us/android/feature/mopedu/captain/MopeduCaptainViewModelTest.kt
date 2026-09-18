package com.us.android.feature.mopedu.captain

import com.google.common.truth.Truth.assertThat
import com.us.android.core.mobility.model.MoneyPaise
import com.us.android.core.mobility.model.PaymentMethod
import com.us.android.core.mobility.model.RidePaymentStatus
import com.us.android.core.mobility.model.RideStatus
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.mopedu.captain.data.CaptainError
import com.us.android.feature.mopedu.captain.data.CaptainResult
import com.us.android.feature.mopedu.captain.home.DutyState
import com.us.android.feature.mopedu.captain.location.CaptainDuty
import com.us.android.feature.mopedu.captain.location.DutyStatus
import com.us.android.feature.mopedu.captain.location.OfflineReason
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

/**
 * Only [runCurrent] and [advanceTimeBy] here, never advanceUntilIdle: an
 * online captain polls for offers forever, and a poll that never goes idle
 * would spin the virtual clock forever.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class MopeduCaptainViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @get:Rule
    val main = MainDispatcherRule(dispatcher)

    private val repo = FakeCaptainRepository()
    private val duty = CaptainDuty()
    private val disclosure = FakeDisclosureStore()
    private val clock = FixedClock(now = 10_000L)

    private fun vm() = MopeduCaptainViewModel(repo, duty, disclosure, clock)

    private fun ready() {
        repo.profileAnswer = CaptainResult.Success(approvedProfile())
        repo.subscriptionAnswer = CaptainResult.Success(trial())
    }

    private fun TestScope.events(model: MopeduCaptainViewModel): List<CaptainEvent> {
        val seen = mutableListOf<CaptainEvent>()
        backgroundScope.launch(dispatcher) { model.events.collect { seen += it } }
        return seen
    }

    /** Home, online, with the service reporting itself running. */
    private fun TestScope.online(model: MopeduCaptainViewModel): List<CaptainEvent> {
        val seen = events(model)
        runCurrent()
        disclosure.markAccepted()
        model.onGoOnlineTapped(permissionGranted = true)
        runCurrent()
        duty.report(DutyStatus.Online(lastPingOk = null))
        runCurrent()
        return seen
    }

    private fun TestScope.onRide(model: MopeduCaptainViewModel, method: PaymentMethod): CaptainUiState.TripInProgress {
        online(model)
        repo.offersAnswer = CaptainResult.Success(listOf(offer(paymentMethod = method)))
        advanceTimeBy(3_100)
        runCurrent()
        model.acceptOffer()
        runCurrent()
        model.markArrived()
        runCurrent()
        model.onOtpInputChanged("1234")
        model.verifyOtpAndStart()
        runCurrent()
        return model.uiState.value as CaptainUiState.TripInProgress
    }

    @Test
    fun `a draft profile opens onboarding at the vehicle step`() = runTest(dispatcher) {
        repo.profileAnswer = CaptainResult.Success(draftProfile())
        val model = vm()
        runCurrent()
        val state = model.uiState.value as CaptainUiState.Onboarding
        assertThat(state.step).isEqualTo(OnboardingStep.VEHICLE)
        assertThat(state.isLoading).isFalse()
    }

    @Test
    fun `no profile at all starts at the profile step, and an approved captain without a plan at the plan step`() = runTest(dispatcher) {
        val first = vm()
        runCurrent()
        assertThat((first.uiState.value as CaptainUiState.Onboarding).step).isEqualTo(OnboardingStep.PROFILE)

        repo.profileAnswer = CaptainResult.Success(approvedProfile())
        val second = vm()
        runCurrent()
        assertThat((second.uiState.value as CaptainUiState.Onboarding).step).isEqualTo(OnboardingStep.SUBSCRIPTION)
    }

    @Test
    fun `an approved captain with a usable plan lands on Home offline, with today's numbers`() = runTest(dispatcher) {
        ready()
        val model = vm()
        runCurrent()
        val home = model.uiState.value as CaptainUiState.Home
        assertThat(home.isOnline).isFalse()
        assertThat(home.duty).isEqualTo(DutyState.Offline())
        assertThat(home.stats.todayEarnings).isEqualTo(MoneyPaise(45000L))
        assertThat(home.stats.rating).isEqualTo(4.8)
        assertThat(repo.onlineCalls).isEmpty()
    }

    @Test
    fun `a server that still thinks the captain is online is put right on start`() = runTest(dispatcher) {
        ready()
        repo.profileAnswer = CaptainResult.Success(approvedProfile(isOnline = true))
        vm()
        runCurrent()
        assertThat(repo.onlineCalls).containsExactly(false)
    }

    @Test
    fun `go online runs disclosure, permission, server, then the service - in that order`() = runTest(dispatcher) {
        ready()
        val model = vm()
        val seen = events(model)
        runCurrent()

        model.onGoOnlineTapped(permissionGranted = false)
        assertThat((model.uiState.value as CaptainUiState.Home).duty).isEqualTo(DutyState.ShowingDisclosure)
        assertThat(seen).isEmpty()
        assertThat(repo.onlineCalls).isEmpty()

        model.onDisclosureAccepted(permissionGranted = false)
        runCurrent()
        assertThat(seen).containsExactly(CaptainEvent.RequestLocationPermission)
        assertThat((model.uiState.value as CaptainUiState.Home).duty).isEqualTo(DutyState.AwaitingPermission)

        model.onPermissionResult(granted = true, canAskAgain = true)
        runCurrent()
        assertThat(repo.onlineCalls).containsExactly(true)
        assertThat(seen).containsExactly(CaptainEvent.RequestLocationPermission, CaptainEvent.StartLocationService).inOrder()
        assertThat((model.uiState.value as CaptainUiState.Home).duty).isEqualTo(DutyState.Online)

        duty.report(DutyStatus.Online(lastPingOk = true))
        runCurrent()
        assertThat((model.uiState.value as CaptainUiState.Home).lastPingOk).isTrue()
        model.onGoOfflineTapped()
        runCurrent()
    }

    @Test
    fun `a denied permission never starts the service, and a refusing server goes back offline`() = runTest(dispatcher) {
        ready()
        val model = vm()
        val seen = events(model)
        runCurrent()
        model.onGoOnlineTapped(permissionGranted = false)
        model.onDisclosureAccepted(permissionGranted = false)
        model.onPermissionResult(granted = false, canAskAgain = false)
        runCurrent()
        assertThat((model.uiState.value as CaptainUiState.Home).duty).isEqualTo(DutyState.PermissionDenied(canAskAgain = false))
        assertThat(seen).doesNotContain(CaptainEvent.StartLocationService)

        repo.setOnlineAnswer = CaptainResult.Failure(CaptainError.Refused(409, "SUBSCRIPTION_EXPIRED", "Your plan has expired."))
        model.onGoOnlineTapped(permissionGranted = true)
        runCurrent()
        val home = model.uiState.value as CaptainUiState.Home
        assertThat(home.duty).isEqualTo(DutyState.Offline(OfflineReason.SERVER_REFUSED))
        assertThat(home.errorMessage).isEqualTo("Your plan has expired.")
        assertThat(seen).doesNotContain(CaptainEvent.StartLocationService)
    }

    @Test
    fun `the service stopping on its own is explained on Home`() = runTest(dispatcher) {
        ready()
        val model = vm()
        online(model)
        duty.report(DutyStatus.Offline(OfflineReason.NO_FIX))
        runCurrent()
        val home = model.uiState.value as CaptainUiState.Home
        assertThat(home.duty).isEqualTo(DutyState.Offline(OfflineReason.NO_FIX))
        assertThat(home.errorMessage).contains("lost your location")
    }

    @Test
    fun `an offer arrives by polling while online, counts down, and expires`() = runTest(dispatcher) {
        ready()
        val model = vm()
        online(model)
        repo.offersAnswer = CaptainResult.Success(listOf(offer(expiresAtEpochMs = 25_000L)))
        advanceTimeBy(3_100)
        runCurrent()
        val home = model.uiState.value as CaptainUiState.Home
        assertThat(home.incomingOffer?.id).isEqualTo("offer-1")
        assertThat(home.offerSecondsLeft).isEqualTo(15L)

        clock.now = 20_000L
        advanceTimeBy(1_100)
        runCurrent()
        assertThat((model.uiState.value as CaptainUiState.Home).offerSecondsLeft).isEqualTo(5L)

        clock.now = 26_000L
        repo.offersAnswer = CaptainResult.Success(emptyList())
        advanceTimeBy(1_100)
        runCurrent()
        assertThat((model.uiState.value as CaptainUiState.Home).incomingOffer).isNull()
        model.onGoOfflineTapped()
        runCurrent()
    }

    @Test
    fun `an already-expired offer is never shown, and declining one tells the server`() = runTest(dispatcher) {
        ready()
        val model = vm()
        online(model)
        repo.offersAnswer = CaptainResult.Success(listOf(offer(expiresAtEpochMs = 9_000L)))
        advanceTimeBy(3_100)
        runCurrent()
        assertThat((model.uiState.value as CaptainUiState.Home).incomingOffer).isNull()

        repo.offersAnswer = CaptainResult.Success(listOf(offer()))
        advanceTimeBy(3_100)
        runCurrent()
        assertThat((model.uiState.value as CaptainUiState.Home).incomingOffer).isNotNull()
        model.rejectOffer()
        runCurrent()
        assertThat(repo.rejected).containsExactly("offer-1")
        assertThat((model.uiState.value as CaptainUiState.Home).incomingOffer).isNull()
        model.onGoOfflineTapped()
        runCurrent()
    }

    @Test
    fun `accept, arrive, verify the OTP and start the trip`() = runTest(dispatcher) {
        ready()
        val model = vm()
        val trip = onRide(model, PaymentMethod.CASH)
        assertThat(repo.accepted).containsExactly("offer-1")
        assertThat(repo.arrived).containsExactly("ride-123")
        assertThat(repo.started).containsExactly("ride-123" to "1234")
        assertThat(trip.booking.id).isEqualTo("ride-123")
        assertThat(trip.booking.status).isEqualTo(RideStatus.IN_PROGRESS)
        assertThat(duty.onRide.value).isTrue()
    }

    @Test
    fun `a short or wrong OTP does not start the trip`() = runTest(dispatcher) {
        ready()
        val model = vm()
        online(model)
        repo.offersAnswer = CaptainResult.Success(listOf(offer()))
        advanceTimeBy(3_100)
        runCurrent()
        model.acceptOffer()
        runCurrent()
        model.markArrived()
        runCurrent()

        model.onOtpInputChanged("12")
        model.verifyOtpAndStart()
        runCurrent()
        assertThat(repo.started).isEmpty()
        assertThat((model.uiState.value as CaptainUiState.VerifyOtp).errorMessage).contains("4-digit")

        repo.otpAnswer = CaptainResult.Failure(CaptainError.Refused(422, "OTP_MISMATCH", "wrong"))
        model.onOtpInputChanged("9999")
        model.verifyOtpAndStart()
        runCurrent()
        val state = model.uiState.value as CaptainUiState.VerifyOtp
        assertThat(state.isVerifying).isFalse()
        assertThat(state.errorMessage).contains("didn't match")
    }

    @Test
    fun `a cash ride is settled by the captain's confirmation`() = runTest(dispatcher) {
        ready()
        val model = vm()
        onRide(model, PaymentMethod.CASH)
        model.completeTrip()
        runCurrent()
        val collect = model.uiState.value as CaptainUiState.CollectPayment
        assertThat(collect.phase).isEqualTo(CollectPhase.Cash(MoneyPaise(9500L)))
        assertThat(duty.onRide.value).isFalse()

        model.confirmCashPayment()
        runCurrent()
        assertThat(repo.cashConfirmed).containsExactly("ride-123")
        assertThat((model.uiState.value as CaptainUiState.CollectPayment).phase).isEqualTo(CollectPhase.Cash(MoneyPaise(9500L), confirmed = true))

        advanceTimeBy(2_000)
        runCurrent()
        assertThat(model.uiState.value).isInstanceOf(CaptainUiState.Home::class.java)
        assertThat((model.uiState.value as CaptainUiState.Home).isOnline).isTrue()
        model.onGoOfflineTapped()
        runCurrent()
    }

    @Test
    fun `a UPI ride waits for the customer and is paid only when the server says so`() = runTest(dispatcher) {
        ready()
        val model = vm()
        onRide(model, PaymentMethod.UPI)
        repo.paymentAnswer = CaptainResult.Success(payment(PaymentMethod.UPI, RidePaymentStatus.CONFIRMING))
        model.completeTrip()
        runCurrent()
        assertThat((model.uiState.value as CaptainUiState.CollectPayment).phase)
            .isEqualTo(CollectPhase.WaitingForCustomer(PaymentMethod.UPI, MoneyPaise(9500L)))

        // Two polls, both confirming: still waiting. The captain has no way to mark this paid.
        advanceTimeBy(6_100)
        runCurrent()
        assertThat((model.uiState.value as CaptainUiState.CollectPayment).phase).isInstanceOf(CollectPhase.WaitingForCustomer::class.java)
        assertThat(repo.paymentReads).isEqualTo(2)
        model.confirmCashPayment() // a no-op for an online fare
        runCurrent()
        assertThat(repo.cashConfirmed).isEmpty()

        repo.paymentAnswer = CaptainResult.Success(payment(PaymentMethod.UPI, RidePaymentStatus.PAID))
        advanceTimeBy(3_100)
        runCurrent()
        assertThat((model.uiState.value as CaptainUiState.CollectPayment).phase).isEqualTo(CollectPhase.OnlinePaid(PaymentMethod.UPI, MoneyPaise(9500L)))

        advanceTimeBy(2_000)
        runCurrent()
        assertThat(model.uiState.value).isInstanceOf(CaptainUiState.Home::class.java)
        model.onGoOfflineTapped()
        runCurrent()
    }

    @Test
    fun `a customer switching to cash moves the captain to collecting cash, and a failure is left to the customer`() = runTest(dispatcher) {
        ready()
        val model = vm()
        onRide(model, PaymentMethod.CARD)
        repo.paymentAnswer = CaptainResult.Success(payment(PaymentMethod.CARD, RidePaymentStatus.FAILED))
        model.completeTrip()
        runCurrent()
        advanceTimeBy(3_100)
        runCurrent()
        assertThat((model.uiState.value as CaptainUiState.CollectPayment).phase).isEqualTo(CollectPhase.OnlineFailed(PaymentMethod.CARD, MoneyPaise(9500L)))

        repo.paymentAnswer = CaptainResult.Success(payment(PaymentMethod.CASH, RidePaymentStatus.CASH_PENDING))
        advanceTimeBy(3_100)
        runCurrent()
        assertThat((model.uiState.value as CaptainUiState.CollectPayment).phase).isEqualTo(CollectPhase.Cash(MoneyPaise(9500L)))
        model.finishRide()
        runCurrent()
        assertThat(model.uiState.value).isInstanceOf(CaptainUiState.Home::class.java)
        model.onGoOfflineTapped()
        runCurrent()
    }
}
