package com.us.android.feature.mopedu.captain

import com.google.common.truth.Truth.assertThat
import com.us.android.core.mobility.model.MoneyPaise
import com.us.android.core.mobility.model.PaymentMethod
import com.us.android.core.mobility.model.RidePaymentStatus
import com.us.android.core.mobility.model.RideStatus
import androidx.lifecycle.SavedStateHandle
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.payments.PaymentHandoffEvent
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.mopedu.captain.data.CaptainError
import com.us.android.feature.mopedu.captain.data.CaptainResult
import com.us.android.feature.mopedu.captain.data.PartnerProfileDto
import com.us.android.feature.mopedu.captain.data.PartnerReviewDto
import com.us.android.feature.mopedu.captain.data.ReviewState
import com.us.android.feature.mopedu.captain.data.SubscriptionPaymentStatus
import com.us.android.feature.mopedu.captain.data.toReview
import com.us.android.feature.mopedu.captain.home.DutyState
import com.us.android.feature.mopedu.captain.location.CaptainDuty
import com.us.android.feature.mopedu.captain.location.DutyStatus
import com.us.android.feature.mopedu.captain.location.OfflineReason
import com.us.android.feature.mopedu.captain.navigation.CaptainDeepLink
import com.us.android.feature.mopedu.captain.navigation.CaptainDeepLinkBus
import com.us.android.feature.mopedu.captain.payment.MOPEDU_PAYMENT_APPLICATION_ID
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
    private val handoff = PaymentHandoff()
    private val deepLinks = CaptainDeepLinkBus()

    private fun vm(handle: SavedStateHandle = SavedStateHandle()) =
        MopeduCaptainViewModel(repo, duty, disclosure, clock, handoff, confirmingOnlyCoordinator(), deepLinks, handle)

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
    fun `no profile at all starts at the profile step, and an approved captain without a plan on the plans`() = runTest(dispatcher) {
        val first = vm()
        runCurrent()
        assertThat((first.uiState.value as CaptainUiState.Onboarding).step).isEqualTo(OnboardingStep.PROFILE)

        repo.profileAnswer = CaptainResult.Success(approvedProfile())
        val second = vm()
        runCurrent()
        val plans = second.uiState.value as CaptainUiState.Plans
        assertThat(plans.reason).isEqualTo(PlansReason.FIRST_PLAN)
        assertThat(plans.canGoBack).isFalse()
        assertThat(plans.trialAvailable).isTrue()
        assertThat(plans.plans).containsExactly(TRIAL_PLAN, MONTHLY_PLAN)
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

    // ── The plans (2026-09-18) ──────────────────────────────────────────

    private fun approvedWithoutPlan() {
        repo.profileAnswer = CaptainResult.Success(approvedProfile())
        repo.subscriptionAnswer = CaptainResult.Success(null)
    }

    private fun plans(model: MopeduCaptainViewModel) = model.uiState.value as CaptainUiState.Plans

    @Test
    fun `the trial is one tap, granted by the server on the spot, and Home follows`() = runTest(dispatcher) {
        approvedWithoutPlan()
        repo.checkoutAnswer = CaptainResult.Success(trialCheckout())
        val model = vm()
        runCurrent()
        assertThat(plans(model).trialAvailable).isTrue()

        repo.subscriptionAnswer = CaptainResult.Success(trial())
        model.startTrial()
        runCurrent()

        assertThat(repo.checkouts).containsExactly("trial_7d" to PaymentMethod.UPI)
        assertThat(model.uiState.value).isInstanceOf(CaptainUiState.Home::class.java)
        assertThat((model.uiState.value as CaptainUiState.Home).subscription).isEqualTo(trial())
        assertThat(model.activeAttempt()).isNull()
        assertThat(repo.subscriptionPaymentReads).isEqualTo(0) // nothing to confirm: no sheet was involved
    }

    @Test
    fun `the trial is offered once ever - a captain who has held any plan cannot start it again`() = runTest(dispatcher) {
        repo.profileAnswer = CaptainResult.Success(approvedProfile())
        // An expired trial, held in the past.
        repo.subscriptionAnswer = CaptainResult.Success(trial(expiresAt = isoAt(clock.now - DAY), status = "expired"))
        val model = vm()
        runCurrent()
        val home = model.uiState.value as CaptainUiState.Home
        assertThat(home.renewal).isEqualTo(RenewalNotice.Expired)
        assertThat(home.isBlocked).isTrue()

        model.renewPlan()
        runCurrent()
        val plans = plans(model)
        assertThat(plans.reason).isEqualTo(PlansReason.EXPIRED)
        assertThat(plans.trialAvailable).isFalse()

        model.startTrial()
        runCurrent()
        assertThat(repo.checkouts).isEmpty()
        assertThat(plans(model).errorMessage).contains("once")
        assertThat(plans(model).phase).isEqualTo(PlanPhase.Choosing)

        // Spelling the code out does not get round it either.
        model.choosePlan("trial_7d", PaymentMethod.UPI)
        runCurrent()
        assertThat(repo.checkouts).isEmpty()
    }

    @Test
    fun `a server that refuses the trial leaves the plans, with nothing active`() = runTest(dispatcher) {
        approvedWithoutPlan()
        repo.checkoutAnswer = CaptainResult.Failure(CaptainError.Refused(409, "trial_used", "Trial already used"))
        val model = vm()
        runCurrent()
        model.startTrial()
        runCurrent()
        val failed = plans(model).phase as PlanPhase.Failed
        assertThat(failed.planCode).isEqualTo("trial_7d")
        assertThat(failed.reason).isEqualTo("Trial already used")
        assertThat(model.uiState.value).isInstanceOf(CaptainUiState.Plans::class.java)
    }

    @Test
    fun `a paid plan is active ONLY when the status source says paid - never from the sheet closing`() = runTest(dispatcher) {
        approvedWithoutPlan()
        val model = vm()
        runCurrent()

        model.choosePlan("bike_monthly", PaymentMethod.CARD)
        runCurrent()
        val opening = plans(model).phase as PlanPhase.OpeningSheet
        assertThat(repo.checkouts).containsExactly("bike_monthly" to PaymentMethod.CARD)
        assertThat(opening.request.attempt.applicationId).isEqualTo(MOPEDU_PAYMENT_APPLICATION_ID)
        assertThat(opening.request.attempt.referenceId).isEqualTo("sub-2")
        assertThat(opening.request.session.providerOrderId).isEqualTo("order_rzp_1")
        assertThat(opening.request.session.amountMinor).isEqualTo(49_900L)
        assertThat(model.activeAttempt()).isEqualTo(opening.request.attempt)
        assertThat(plans(model).canGoBack).isFalse()

        // The sheet ends (the SDK may even have said "success"): the server is asked, and says pending.
        repo.subscriptionPaymentAnswers += CaptainResult.Success(subscriptionPayment(SubscriptionPaymentStatus.PENDING))
        handoff.publish(PaymentHandoffEvent.SheetClosed(opening.request.attempt))
        runCurrent()
        assertThat(plans(model).phase).isInstanceOf(PlanPhase.Confirming::class.java)
        advanceTimeBy(3_500) // reads at 1 s and 3 s: both pending
        runCurrent()
        assertThat(plans(model).phase).isInstanceOf(PlanPhase.Confirming::class.java)
        assertThat(repo.subscriptionPaymentReads).isEqualTo(2)
        assertThat(model.uiState.value).isInstanceOf(CaptainUiState.Plans::class.java)

        // Now the server says paid: active, and Home.
        repo.subscriptionPaymentAnswers.clear()
        repo.subscriptionPaymentAnswers += CaptainResult.Success(subscriptionPayment(SubscriptionPaymentStatus.PAID))
        repo.subscriptionAnswer = CaptainResult.Success(monthlyPlan(expiresAt = isoAt(clock.now + 30 * DAY)))
        advanceTimeBy(3_100) // the 6 s read
        runCurrent()
        assertThat(plans(model).phase).isEqualTo(PlanPhase.Activated)
        assertThat(model.activeAttempt()).isNull()
        advanceTimeBy(1_600)
        runCurrent()
        val home = model.uiState.value as CaptainUiState.Home
        assertThat(home.subscription?.planCode).isEqualTo("bike_monthly")
        assertThat(home.renewal).isEqualTo(RenewalNotice.None)
    }

    @Test
    fun `a sheet that never opened is a failure with nothing charged, and the attempt is forgotten`() = runTest(dispatcher) {
        approvedWithoutPlan()
        val model = vm()
        runCurrent()
        model.choosePlan("bike_monthly", PaymentMethod.UPI)
        runCurrent()
        val attempt = model.activeAttempt()!!

        handoff.publish(PaymentHandoffEvent.Unavailable(attempt, "no network"))
        runCurrent()

        val failed = plans(model).phase as PlanPhase.Failed
        assertThat(failed.planCode).isEqualTo("bike_monthly")
        assertThat(failed.method).isEqualTo(PaymentMethod.UPI)
        assertThat(failed.reason).isEqualTo("no network")
        assertThat(failed.retryable).isTrue()
        assertThat(model.activeAttempt()).isNull()
        assertThat(repo.subscriptionPaymentReads).isEqualTo(0)
    }

    @Test
    fun `an ending for another attempt - an older one, or another application's - is ignored`() = runTest(dispatcher) {
        approvedWithoutPlan()
        val model = vm()
        runCurrent()
        model.choosePlan("bike_monthly", PaymentMethod.UPI)
        runCurrent()
        val mine = model.activeAttempt()!!

        handoff.publish(PaymentHandoffEvent.SheetClosed(mine.copy(id = "an-older-attempt")))
        handoff.publish(PaymentHandoffEvent.SheetClosed(mine.copy(referenceId = "ride-123"))) // the rider's fare, same application
        handoff.publish(PaymentHandoffEvent.SheetClosed(mine.copy(applicationId = "feast")))
        runCurrent()

        assertThat(plans(model).phase).isInstanceOf(PlanPhase.OpeningSheet::class.java)
        assertThat(repo.subscriptionPaymentReads).isEqualTo(0)
    }

    @Test
    fun `a failed payment is retried with a new checkout, and a timeout keeps confirming rather than failing`() = runTest(dispatcher) {
        approvedWithoutPlan()
        val model = vm()
        runCurrent()
        model.choosePlan("bike_monthly", PaymentMethod.UPI)
        runCurrent()
        val first = model.activeAttempt()!!

        repo.subscriptionPaymentAnswers += CaptainResult.Success(subscriptionPayment(SubscriptionPaymentStatus.FAILED))
        handoff.publish(PaymentHandoffEvent.SheetClosed(first))
        advanceTimeBy(1_100)
        runCurrent()
        val failed = plans(model).phase as PlanPhase.Failed
        assertThat(failed.retryable).isTrue()
        assertThat(model.activeAttempt()).isNull()

        model.choosePlan("bike_monthly", PaymentMethod.UPI)
        runCurrent()
        val second = model.activeAttempt()!!
        assertThat(second).isNotEqualTo(first)
        assertThat(repo.checkouts).hasSize(2)

        // Unreachable reads until the poll gives up: still confirming, never failed, never active.
        repo.subscriptionPaymentAnswers.clear()
        handoff.publish(PaymentHandoffEvent.SheetClosed(second))
        runCurrent()
        advanceTimeBy(181_000)
        runCurrent()
        assertThat(plans(model).phase).isEqualTo(PlanPhase.StillConfirming("bike_monthly"))
        assertThat(model.activeAttempt()).isEqualTo(second)

        repo.subscriptionPaymentAnswers += CaptainResult.Success(subscriptionPayment(SubscriptionPaymentStatus.PAID))
        model.checkPaymentAgain()
        advanceTimeBy(1_100)
        runCurrent()
        assertThat(plans(model).phase).isEqualTo(PlanPhase.Activated)
    }

    @Test
    fun `after process death with a sheet requested, the server is asked and no sheet reopens`() = runTest(dispatcher) {
        approvedWithoutPlan()
        val handle = SavedStateHandle()
        val first = vm(handle)
        runCurrent()
        first.choosePlan("bike_monthly", PaymentMethod.UPI)
        runCurrent()
        val attempt = first.activeAttempt()!!

        repo.subscriptionPaymentAnswers += CaptainResult.Success(subscriptionPayment(SubscriptionPaymentStatus.PAID))
        repo.subscriptionAnswer = CaptainResult.Success(monthlyPlan(expiresAt = isoAt(clock.now + 30 * DAY)))
        val second = vm(handle)
        runCurrent()
        assertThat(plans(second).phase).isInstanceOf(PlanPhase.Confirming::class.java)
        assertThat(second.activeAttempt()).isEqualTo(attempt)

        advanceTimeBy(1_100)
        runCurrent()
        assertThat(plans(second).phase).isEqualTo(PlanPhase.Activated)
        assertThat(repo.checkouts).hasSize(1) // no second checkout, no second sheet
        advanceTimeBy(1_600)
        runCurrent()
        assertThat(second.uiState.value).isInstanceOf(CaptainUiState.Home::class.java)
    }

    // ── Renewal ─────────────────────────────────────────────────────────

    @Test
    fun `the renewal notice thresholds - none beyond three days, a banner within, blocking at zero`() {
        val now = 1_000_000L
        assertThat(renewalNoticeFor(monthlyPlan(isoAt(now + 4 * DAY)), now)).isEqualTo(RenewalNotice.None)
        assertThat(renewalNoticeFor(monthlyPlan(isoAt(now + 3 * DAY + 1)), now)).isEqualTo(RenewalNotice.None)
        assertThat(renewalNoticeFor(monthlyPlan(isoAt(now + 3 * DAY)), now)).isEqualTo(RenewalNotice.ExpiringSoon(3))
        assertThat(renewalNoticeFor(monthlyPlan(isoAt(now + 2 * DAY + DAY / 2)), now)).isEqualTo(RenewalNotice.ExpiringSoon(3))
        assertThat(renewalNoticeFor(monthlyPlan(isoAt(now + 2 * DAY)), now)).isEqualTo(RenewalNotice.ExpiringSoon(2))
        assertThat(renewalNoticeFor(monthlyPlan(isoAt(now + 60 * 60 * 1_000L)), now)).isEqualTo(RenewalNotice.ExpiringSoon(1))
        assertThat(renewalNoticeFor(monthlyPlan(isoAt(now)), now)).isEqualTo(RenewalNotice.Expired)
        assertThat(renewalNoticeFor(monthlyPlan(isoAt(now - 1)), now)).isEqualTo(RenewalNotice.Expired)
        // The server's word outranks the date: not usable is expired however far off the date.
        assertThat(renewalNoticeFor(monthlyPlan(isoAt(now + 30 * DAY), status = "expired"), now)).isEqualTo(RenewalNotice.Expired)
        assertThat(renewalNoticeFor(monthlyPlan(isoAt(now + 30 * DAY), status = "cancelled"), now)).isEqualTo(RenewalNotice.Expired)
        assertThat(renewalNoticeFor(null, now)).isEqualTo(RenewalNotice.Expired)
        // An unparseable date is left to the server's word.
        assertThat(renewalNoticeFor(monthlyPlan("soon"), now)).isEqualTo(RenewalNotice.None)
    }

    @Test
    fun `a plan running out within three days shows the banner, and Home still works`() = runTest(dispatcher) {
        repo.profileAnswer = CaptainResult.Success(approvedProfile())
        repo.subscriptionAnswer = CaptainResult.Success(monthlyPlan(expiresAt = isoAt(clock.now + 2 * DAY)))
        val model = vm()
        runCurrent()
        val home = model.uiState.value as CaptainUiState.Home
        assertThat(home.renewal).isEqualTo(RenewalNotice.ExpiringSoon(2))
        assertThat(home.isBlocked).isFalse()

        model.renewPlan()
        runCurrent()
        val plans = plans(model)
        assertThat(plans.reason).isEqualTo(PlansReason.RENEWAL)
        assertThat(plans.canGoBack).isTrue()
        assertThat(plans.trialAvailable).isFalse()

        model.dismissPlans()
        runCurrent()
        assertThat(model.uiState.value).isInstanceOf(CaptainUiState.Home::class.java)
    }

    @Test
    fun `an expired plan blocks going online until it is renewed`() = runTest(dispatcher) {
        repo.profileAnswer = CaptainResult.Success(approvedProfile())
        repo.subscriptionAnswer = CaptainResult.Success(monthlyPlan(expiresAt = isoAt(clock.now - 1)))
        val model = vm()
        val seen = events(model)
        runCurrent()
        assertThat((model.uiState.value as CaptainUiState.Home).isBlocked).isTrue()

        disclosure.markAccepted()
        model.onGoOnlineTapped(permissionGranted = true)
        runCurrent()
        val home = model.uiState.value as CaptainUiState.Home
        assertThat(home.duty).isEqualTo(DutyState.Offline())
        assertThat(home.errorMessage).contains("Renew")
        assertThat(repo.onlineCalls).isEmpty()
        assertThat(seen).isEmpty()
    }

    // ── Review states ───────────────────────────────────────────────────

    @Test
    fun `under review lands on the status step with what is pending`() = runTest(dispatcher) {
        repo.profileAnswer = CaptainResult.Success(underReviewProfile("driving_license"))
        val model = vm()
        runCurrent()
        val state = model.uiState.value as CaptainUiState.Onboarding
        assertThat(state.step).isEqualTo(OnboardingStep.STATUS)
        assertThat(state.isVerifying).isFalse()
        assertThat(state.review.state).isEqualTo(ReviewState.UNDER_REVIEW)
        assertThat(state.review.pending).containsExactly("driving_license")
    }

    @Test
    fun `incomplete with something pending goes back to the documents step`() = runTest(dispatcher) {
        repo.profileAnswer = CaptainResult.Success(incompleteProfile("selfie"))
        val model = vm()
        runCurrent()
        val state = model.uiState.value as CaptainUiState.Onboarding
        assertThat(state.step).isEqualTo(OnboardingStep.DOCUMENTS)
        assertThat(state.review.pending).containsExactly("selfie")
    }

    @Test
    fun `submitting for verification polls the server - verifying, then the plans once approved`() = runTest(dispatcher) {
        repo.profileAnswer = CaptainResult.Success(incompleteProfile("selfie"))
        val model = vm()
        runCurrent()
        model.submitSelfie("")
        runCurrent()
        assertThat(repo.submittedDocuments).containsExactly("selfie")

        // The first two polls still say incomplete (DigiLocker is finishing); the third approves.
        repo.profileAnswers += CaptainResult.Success(incompleteProfile())
        repo.profileAnswers += CaptainResult.Success(incompleteProfile())
        repo.profileAnswers += CaptainResult.Success(approvedProfile())
        model.submitForVerification()
        runCurrent()
        val verifying = model.uiState.value as CaptainUiState.Onboarding
        assertThat(verifying.step).isEqualTo(OnboardingStep.STATUS)
        assertThat(verifying.isVerifying).isTrue()

        advanceTimeBy(3_100)
        runCurrent()
        assertThat((model.uiState.value as CaptainUiState.Onboarding).isVerifying).isTrue()

        advanceTimeBy(3_100)
        runCurrent()
        val plans = model.uiState.value as CaptainUiState.Plans
        assertThat(plans.reason).isEqualTo(PlansReason.FIRST_PLAN)
    }

    @Test
    fun `verification that ends under review says so, and one that stays incomplete shows what is missing`() = runTest(dispatcher) {
        repo.profileAnswer = CaptainResult.Success(incompleteProfile())
        val model = vm()
        runCurrent()

        repo.profileAnswers += CaptainResult.Success(incompleteProfile())
        repo.profileAnswers += CaptainResult.Success(underReviewProfile("vehicle_rc"))
        model.submitForVerification()
        runCurrent()
        advanceTimeBy(3_100)
        runCurrent()
        val review = model.uiState.value as CaptainUiState.Onboarding
        assertThat(review.step).isEqualTo(OnboardingStep.STATUS)
        assertThat(review.isVerifying).isFalse()
        assertThat(review.review.state).isEqualTo(ReviewState.UNDER_REVIEW)
        assertThat(review.review.pending).containsExactly("vehicle_rc")

        // Still incomplete for the whole window: the status step, not verifying, with the pending items.
        repo.profileAnswers.clear()
        repo.profileAnswer = CaptainResult.Success(incompleteProfile("selfie"))
        model.submitForVerification()
        runCurrent()
        advanceTimeBy(10 * 3_100L)
        runCurrent()
        val stuck = model.uiState.value as CaptainUiState.Onboarding
        assertThat(stuck.step).isEqualTo(OnboardingStep.STATUS)
        assertThat(stuck.isVerifying).isFalse()
        assertThat(stuck.review.state).isEqualTo(ReviewState.INCOMPLETE)
        assertThat(stuck.review.pending).containsExactly("selfie")
    }

    @Test
    fun `the review verdict is tolerant - absent means the legacy fields, and an unknown state is incomplete`() {
        val base = PartnerProfileDto(id = "p-1", status = "approved", kycStatus = "approved")
        assertThat(base.toReview().state).isEqualTo(ReviewState.APPROVED)
        assertThat(base.copy(kycStatus = "pending").toReview().state).isEqualTo(ReviewState.INCOMPLETE)
        assertThat(base.copy(kycStatus = "under_review").toReview().state).isEqualTo(ReviewState.INCOMPLETE)
        // Sent, it is authoritative — whatever the legacy fields say.
        assertThat(base.copy(review = PartnerReviewDto("under_review", listOf("selfie"))).toReview())
            .isEqualTo(com.us.android.feature.mopedu.captain.data.PartnerReview(ReviewState.UNDER_REVIEW, listOf("selfie")))
        assertThat(base.copy(kycStatus = "pending", review = PartnerReviewDto("approved")).toReview().state).isEqualTo(ReviewState.APPROVED)
        assertThat(base.copy(review = PartnerReviewDto("something_new")).toReview().state).isEqualTo(ReviewState.INCOMPLETE)
    }

    // ── Pushes ──────────────────────────────────────────────────────────

    @Test
    fun `a subscription push opens the plans from Home, and never interrupts a ride`() = runTest(dispatcher) {
        ready()
        val model = vm()
        runCurrent()
        assertThat(model.uiState.value).isInstanceOf(CaptainUiState.Home::class.java)

        deepLinks.publish(CaptainDeepLink.Plans)
        runCurrent()
        assertThat(plans(model).reason).isEqualTo(PlansReason.RENEWAL)
        model.dismissPlans()
        runCurrent()

        onRide(model, PaymentMethod.CASH)
        deepLinks.publish(CaptainDeepLink.Plans)
        runCurrent()
        assertThat(model.uiState.value).isInstanceOf(CaptainUiState.TripInProgress::class.java)
        model.completeTrip()
        runCurrent()
        model.finishRide()
        runCurrent()
        model.onGoOfflineTapped()
        runCurrent()
    }

    @Test
    fun `an approval push re-reads the server and leaves onboarding`() = runTest(dispatcher) {
        repo.profileAnswer = CaptainResult.Success(underReviewProfile("driving_license"))
        val model = vm()
        runCurrent()
        assertThat(model.uiState.value).isInstanceOf(CaptainUiState.Onboarding::class.java)

        repo.profileAnswer = CaptainResult.Success(approvedProfile())
        deepLinks.publish(CaptainDeepLink.OnboardingStatus)
        runCurrent()
        assertThat(model.uiState.value).isInstanceOf(CaptainUiState.Plans::class.java)
    }
}
