package com.us.android.feature.mopedu.captain

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.mobility.model.CaptainOffer
import com.us.android.core.mobility.model.CaptainState
import com.us.android.core.mobility.model.MoneyPaise
import com.us.android.core.mobility.model.PartnerDocument
import com.us.android.core.mobility.model.PartnerProfile
import com.us.android.core.mobility.model.PartnerSubscription
import com.us.android.core.mobility.model.PaymentMethod
import com.us.android.core.mobility.model.RideBooking
import com.us.android.core.mobility.model.RidePaymentStatus
import com.us.android.core.mobility.model.RideStatus
import com.us.android.core.mobility.model.SubscriptionPlan
import com.us.android.core.mobility.model.Vehicle
import com.us.android.core.mobility.model.VehicleType
import com.us.android.core.payments.InFlightPayment
import com.us.android.core.payments.PaymentAttempt
import com.us.android.core.payments.PaymentConfirmation
import com.us.android.core.payments.PaymentCoordinator
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.payments.PaymentHandoffEvent
import com.us.android.core.payments.PaymentPollPolicy
import com.us.android.core.payments.PaymentStateStore
import com.us.android.feature.mopedu.captain.data.CaptainResult
import com.us.android.feature.mopedu.captain.data.MopeduCaptainRepository
import com.us.android.feature.mopedu.captain.data.PartnerReview
import com.us.android.feature.mopedu.captain.data.ReviewState
import com.us.android.feature.mopedu.captain.data.toEpochMs
import com.us.android.feature.mopedu.captain.data.userMessage
import com.us.android.feature.mopedu.captain.data.valueOrNull
import com.us.android.feature.mopedu.captain.home.DutyEffect
import com.us.android.feature.mopedu.captain.home.DutyState
import com.us.android.feature.mopedu.captain.home.GoOnlineFlow
import com.us.android.feature.mopedu.captain.home.LocationDisclosureStore
import com.us.android.feature.mopedu.captain.location.CaptainDuty
import com.us.android.feature.mopedu.captain.location.DutyStatus
import com.us.android.feature.mopedu.captain.location.OfflineReason
import com.us.android.feature.mopedu.captain.navigation.CaptainDeepLink
import com.us.android.feature.mopedu.captain.navigation.CaptainDeepLinkBus
import com.us.android.feature.mopedu.captain.payment.CaptainPaymentRequest
import com.us.android.feature.mopedu.captain.payment.MOPEDU_PAYMENT_APPLICATION_ID
import com.us.android.feature.mopedu.captain.payment.SubscriptionPaymentStatusSource
import com.us.android.feature.mopedu.captain.payment.toPaymentSession
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.channels.BufferOverflow
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import javax.inject.Inject

/** A wall clock the offer countdown and the renewal banner read. Injected so tests move it by hand. */
fun interface CaptainClock {
    fun nowMillis(): Long
}

/** Onboarding, in order. The plan is NOT a step: it is its own screen, reached once the review approves. */
enum class OnboardingStep(val stepNumber: Int, val title: String) {
    PROFILE(1, "Profile"),
    VEHICLE(2, "Vehicle"),
    DOCUMENTS(3, "Documents"),
    STATUS(4, "Verification"),
}

/** Why the plans screen is open: it decides the back edge and the heading. */
enum class PlansReason {
    /** An approved captain who has never held a plan. No way back. */
    FIRST_PLAN,

    /** The plan runs out within three days. Back goes to Home. */
    RENEWAL,

    /** The plan has run out; Home is blocked until a new one is paid. */
    EXPIRED,
}

/** Where the plan purchase is. [Activated] is reached ONLY from the server: the trial's `active`, or the status source's `paid`. */
sealed interface PlanPhase {
    data object Choosing : PlanPhase

    /** Checkout in flight for [planCode]. */
    data class Starting(val planCode: String) : PlanPhase

    /** The sheet has been requested from the Activity; its ending arrives on the handoff bus. */
    data class OpeningSheet(val planCode: String, val request: CaptainPaymentRequest) : PlanPhase

    /** The sheet ended — however it ended — and the server is being asked. */
    data class Confirming(val planCode: String?, val elapsedSeconds: Int) : PlanPhase

    data class Failed(val planCode: String?, val method: PaymentMethod, val reason: String?, val retryable: Boolean) : PlanPhase

    /** The poll gave up without an answer. NOT a failure: the payment may still land. */
    data class StillConfirming(val planCode: String?) : PlanPhase

    /** The server says the plan is active. Home follows. */
    data object Activated : PlanPhase
}

/** What Home says about the plan's remaining life. */
sealed interface RenewalNotice {
    data object None : RenewalNotice

    /** Within three days of running out: a banner. */
    data class ExpiringSoon(val daysLeft: Long) : RenewalNotice

    /** Run out, or never usable: a blocking card; going online is refused. */
    data object Expired : RenewalNotice
}

/**
 * The banner threshold: within [RENEWAL_WINDOW_MILLIS] (3 days) of `expires_at`
 * is [RenewalNotice.ExpiringSoon]; at or past it, or a plan the server no longer
 * calls usable, is [RenewalNotice.Expired]. An expiry the app cannot parse is
 * left to the server's word.
 */
fun renewalNoticeFor(subscription: PartnerSubscription?, nowMillis: Long): RenewalNotice {
    if (subscription == null || !subscription.isUsable) return RenewalNotice.Expired
    val expiresAt = subscription.expiresAt.toEpochMs() ?: return RenewalNotice.None
    val remaining = expiresAt - nowMillis
    return when {
        remaining <= 0L -> RenewalNotice.Expired
        remaining <= RENEWAL_WINDOW_MILLIS -> RenewalNotice.ExpiringSoon(daysLeft = (remaining + DAY_MILLIS - 1) / DAY_MILLIS)
        else -> RenewalNotice.None
    }
}

const val DAY_MILLIS = 24L * 60L * 60L * 1_000L
const val RENEWAL_WINDOW_DAYS = 3L
const val RENEWAL_WINDOW_MILLIS = RENEWAL_WINDOW_DAYS * DAY_MILLIS

/** How the fare is being collected after the trip. Online money is settled by the SERVER only. */
sealed interface CollectPhase {
    data class Cash(val amount: MoneyPaise, val isConfirming: Boolean = false, val confirmed: Boolean = false) : CollectPhase

    /** UPI/card: "Waiting for customer payment" until `GET /rides/{id}/payment` says paid. */
    data class WaitingForCustomer(val method: PaymentMethod, val amount: MoneyPaise) : CollectPhase

    data class OnlinePaid(val method: PaymentMethod, val amount: MoneyPaise) : CollectPhase

    /** The customer's payment failed; they may retry or switch to cash on their side. */
    data class OnlineFailed(val method: PaymentMethod, val amount: MoneyPaise) : CollectPhase

    data object Refunding : CollectPhase
}

sealed interface CaptainUiState {
    data object Loading : CaptainUiState

    data class Onboarding(
        val step: OnboardingStep = OnboardingStep.PROFILE,
        val profile: PartnerProfile? = null,
        val vehicle: Vehicle? = null,
        val documents: List<PartnerDocument> = emptyList(),
        /** The server's verdict; INCOMPLETE until it says otherwise. */
        val review: PartnerReview = PartnerReview.INCOMPLETE,
        val isLoading: Boolean = false,
        /** Polling `GET /partners/me` after the documents went in: "Verifying…". */
        val isVerifying: Boolean = false,
        val errorMessage: String? = null,
    ) : CaptainUiState

    /** The plans: the trial one tap, the paid ones through the sheet. */
    data class Plans(
        val plans: List<SubscriptionPlan> = emptyList(),
        val subscription: PartnerSubscription? = null,
        val reason: PlansReason = PlansReason.FIRST_PLAN,
        val phase: PlanPhase = PlanPhase.Choosing,
        val isLoading: Boolean = false,
        val errorMessage: String? = null,
    ) : CaptainUiState {
        /** The trial is granted once ever: never to a captain who has held any plan, in any state. */
        val trialAvailable: Boolean get() = subscription == null

        val canGoBack: Boolean
            get() = reason != PlansReason.FIRST_PLAN && phase !is PlanPhase.OpeningSheet && phase !is PlanPhase.Confirming
    }

    /** Offline or online-and-waiting: the duty toggle, today's numbers and, when one arrives, the offer card. */
    data class Home(
        val stats: CaptainState,
        val profile: PartnerProfile? = null,
        val subscription: PartnerSubscription? = null,
        val renewal: RenewalNotice = RenewalNotice.None,
        val duty: DutyState = DutyState.Offline(),
        val lastPingOk: Boolean? = null,
        val incomingOffer: CaptainOffer? = null,
        val offerSecondsLeft: Long = 0,
        val isAccepting: Boolean = false,
        val errorMessage: String? = null,
    ) : CaptainUiState {
        val isOnline: Boolean get() = duty == DutyState.Online

        /** An expired plan blocks going online; the card says so. */
        val isBlocked: Boolean get() = renewal == RenewalNotice.Expired
    }

    data class EnRouteToPickup(val booking: RideBooking, val isArriving: Boolean = false, val errorMessage: String? = null) : CaptainUiState

    data class VerifyOtp(
        val booking: RideBooking,
        val otpInput: String = "",
        val isVerifying: Boolean = false,
        val errorMessage: String? = null,
    ) : CaptainUiState

    data class TripInProgress(val booking: RideBooking, val isCompleting: Boolean = false, val errorMessage: String? = null) : CaptainUiState

    data class CollectPayment(val booking: RideBooking, val phase: CollectPhase, val errorMessage: String? = null) : CaptainUiState
}

/** One-shot instructions only the screen can carry out. */
sealed interface CaptainEvent {
    data object RequestLocationPermission : CaptainEvent

    data object StartLocationService : CaptainEvent

    /** DigiLocker's page: the screen opens it in the browser. */
    data class OpenDigiLocker(val url: String) : CaptainEvent
}

/**
 * The captain's day: the onboarding gate (profile, vehicle, documents, then a
 * "Verifying…" poll of `GET /partners/me` that ends in Home, the plans or
 * "Under review"), the plans (the trial one tap; a paid plan through
 * `:core:payments` stamped "mopedu", active ONLY when
 * `GET /subscriptions/me/payment` says paid), then Home with the Go-online
 * toggle ([GoOnlineFlow]: disclosure → permission → server → service), offers
 * with a countdown, the ride (en route, OTP, trip) and collecting the fare.
 *
 * Cash is settled by the captain's confirmation. UPI/card is settled ONLY when
 * `GET /rides/{id}/payment` says paid: the card reads "Waiting for customer
 * payment" until then, and nothing on this device can mark it paid.
 */
@HiltViewModel
@Suppress("TooManyFunctions", "LargeClass", "LongParameterList")
class MopeduCaptainViewModel @Inject constructor(
    private val repository: MopeduCaptainRepository,
    private val duty: CaptainDuty,
    private val disclosure: LocationDisclosureStore,
    private val clock: CaptainClock,
    private val handoff: PaymentHandoff,
    private val payments: PaymentCoordinator,
    private val deepLinks: CaptainDeepLinkBus,
    savedState: SavedStateHandle,
) : ViewModel() {

    private val flow = GoOnlineFlow()
    private val saved = CaptainContinuation(savedState)
    private val statusSource = SubscriptionPaymentStatusSource(repository)
    private val policy = PaymentPollPolicy()

    private val _uiState = MutableStateFlow<CaptainUiState>(CaptainUiState.Loading)
    val uiState: StateFlow<CaptainUiState> = _uiState.asStateFlow()

    private val _events = MutableSharedFlow<CaptainEvent>(extraBufferCapacity = 4, onBufferOverflow = BufferOverflow.DROP_OLDEST)
    val events: SharedFlow<CaptainEvent> = _events.asSharedFlow()

    private var stats: CaptainState = CaptainState(false, null, PartnerProfile.DEFAULT_RATING, 0, MoneyPaise.ZERO)
    private var profile: PartnerProfile? = null
    private var review: PartnerReview = PartnerReview.INCOMPLETE
    private var subscription: PartnerSubscription? = null
    private var plans: List<SubscriptionPlan> = emptyList()
    private var offerJob: Job? = null
    private var countdownJob: Job? = null
    private var paymentJob: Job? = null
    private var verifyJob: Job? = null
    private var confirmJob: Job? = null

    init {
        viewModelScope.launch { duty.status.collect(::onDutyStatus) }
        observeHandoff()
        observeDeepLinks()
        val pending = saved.inFlight.attempt
        if (pending != null) resumePlanPayment(pending) else checkInitialStatus()
    }

    // ── The gate ────────────────────────────────────────────────────────

    fun checkInitialStatus() {
        viewModelScope.launch { route() }
    }

    /**
     * Where the captain belongs right now, from the server: onboarding until the
     * review approves; the plans until one is held; Home from then on (an expired
     * plan blocks Home rather than sending the captain back through onboarding).
     */
    private suspend fun route() {
        when (val result = repository.profile()) {
            is CaptainResult.Failure -> loadOnboardingNow(OnboardingStep.PROFILE)
            is CaptainResult.Success -> {
                val captain = result.value
                profile = captain.profile
                review = captain.review
                when (captain.review.state) {
                    ReviewState.INCOMPLETE -> loadOnboardingNow(nextStepFor(captain.profile, captain.review))
                    ReviewState.UNDER_REVIEW -> loadOnboardingNow(OnboardingStep.STATUS)
                    ReviewState.APPROVED -> {
                        subscription = repository.mySubscription().valueOrNull()
                        if (subscription == null) {
                            openPlansNow(PlansReason.FIRST_PLAN)
                            return
                        }
                        // The server still thinks the captain is online, but nothing on
                        // this device is sharing location (the app was killed): put it right.
                        if (captain.isOnline && !duty.isOnline) repository.setOnline(false)
                        showHome()
                        refreshEarnings()
                    }
                }
            }
        }
    }

    private fun nextStepFor(profile: PartnerProfile, review: PartnerReview): OnboardingStep = when {
        profile.status == DRAFT -> OnboardingStep.VEHICLE
        review.pending.isNotEmpty() || profile.kycStatus == KYC_PENDING -> OnboardingStep.DOCUMENTS
        else -> OnboardingStep.STATUS
    }

    private fun loadOnboarding(step: OnboardingStep) {
        viewModelScope.launch { loadOnboardingNow(step) }
    }

    private suspend fun loadOnboardingNow(step: OnboardingStep) {
        verifyJob?.cancel()
        _uiState.value = CaptainUiState.Onboarding(step = step, profile = profile, review = review, isLoading = true)
        repository.profile().valueOrNull()?.let {
            profile = it.profile
            review = it.review
        }
        val vehicles = repository.vehicles().valueOrNull().orEmpty()
        val docs = repository.documents().valueOrNull().orEmpty()
        _uiState.value = CaptainUiState.Onboarding(
            step = step,
            profile = profile,
            vehicle = vehicles.firstOrNull(),
            documents = docs,
            review = review,
            isLoading = false,
        )
    }

    private fun onboardingAction(failure: String, action: suspend () -> CaptainResult<*>, next: OnboardingStep) {
        val current = _uiState.value as? CaptainUiState.Onboarding ?: return
        _uiState.value = current.copy(isLoading = true, errorMessage = null)
        viewModelScope.launch {
            when (val result = action()) {
                is CaptainResult.Success -> loadOnboardingNow(next)
                is CaptainResult.Failure -> _uiState.value = current.copy(isLoading = false, errorMessage = "$failure: ${result.error.userMessage()}")
            }
        }
    }

    fun submitProfile(fullName: String, phone: String, email: String?) =
        onboardingAction("Profile not saved", { repository.createProfile(fullName, phone, email) }, OnboardingStep.VEHICLE)

    fun submitVehicle(type: VehicleType, regNumber: String, brand: String, model: String) =
        onboardingAction("Vehicle not saved", { repository.addVehicle(type, regNumber, brand.ifBlank { null }, model.ifBlank { null }) }, OnboardingStep.DOCUMENTS)

    fun submitDocument(type: String, number: String, fileUrl: String) =
        onboardingAction("Document not submitted", { repository.submitDocument(type, number.ifBlank { null }, fileUrl) }, OnboardingStep.DOCUMENTS)

    /** The selfie is a document of type `selfie`; the review needs it beside the DigiLocker Aadhaar. */
    fun submitSelfie(fileUrl: String) =
        onboardingAction("Selfie not submitted", { repository.submitDocument(DOCUMENT_SELFIE, null, fileUrl) }, OnboardingStep.DOCUMENTS)

    /** Starts DigiLocker: the screen opens its page; the documents step then shows the Aadhaar's status. */
    fun startDigiLocker() {
        val current = _uiState.value as? CaptainUiState.Onboarding ?: return
        _uiState.value = current.copy(isLoading = true, errorMessage = null)
        viewModelScope.launch {
            when (val result = repository.startAadhaar()) {
                is CaptainResult.Success -> {
                    result.value.digiLockerUrl.takeIf { it.isNotBlank() }?.let { _events.tryEmit(CaptainEvent.OpenDigiLocker(it)) }
                    loadOnboardingNow(OnboardingStep.DOCUMENTS)
                }
                is CaptainResult.Failure -> _uiState.value = current.copy(isLoading = false, errorMessage = "DigiLocker not started: ${result.error.userMessage()}")
            }
        }
    }

    /**
     * The documents are in: "Verifying…" while `GET /partners/me` is polled.
     * DigiLocker-verified documents approve on their own and land on the
     * plans (or Home); manually uploaded ones end in "Under review" with what
     * is pending. Nothing here waits on a person unless the server says so.
     */
    fun submitForVerification() {
        val current = _uiState.value as? CaptainUiState.Onboarding ?: return
        verifyJob?.cancel()
        _uiState.value = current.copy(step = OnboardingStep.STATUS, isLoading = false, isVerifying = true, errorMessage = null)
        verifyJob = viewModelScope.launch {
            repeat(VERIFY_MAX_POLLS) { poll ->
                if (poll > 0) delay(VERIFY_POLL_MILLIS)
                val captain = repository.profile().valueOrNull() ?: return@repeat
                profile = captain.profile
                review = captain.review
                when (captain.review.state) {
                    ReviewState.APPROVED -> {
                        route()
                        return@launch
                    }
                    ReviewState.UNDER_REVIEW -> {
                        loadOnboardingNow(OnboardingStep.STATUS)
                        return@launch
                    }
                    ReviewState.INCOMPLETE -> Unit
                }
            }
            // Still incomplete after the window: show what is missing, with a way back.
            loadOnboardingNow(OnboardingStep.STATUS)
        }
    }

    /** Asks the server again and lands wherever it says: approved captains leave onboarding here. */
    fun refreshOnboardingStatus() = checkInitialStatus()

    fun openOnboarding(step: OnboardingStep = OnboardingStep.STATUS) = loadOnboarding(step)

    fun proceedToConsole() = checkInitialStatus()

    // ── The plans ───────────────────────────────────────────────────────

    fun openPlans(reason: PlansReason = plansReasonNow()) {
        viewModelScope.launch { openPlansNow(reason) }
    }

    private fun plansReasonNow(): PlansReason = when {
        subscription == null -> PlansReason.FIRST_PLAN
        renewalNoticeFor(subscription, clock.nowMillis()) == RenewalNotice.Expired -> PlansReason.EXPIRED
        else -> PlansReason.RENEWAL
    }

    private suspend fun openPlansNow(reason: PlansReason, phase: PlanPhase = PlanPhase.Choosing) {
        _uiState.value = CaptainUiState.Plans(plans = plans, subscription = subscription, reason = reason, phase = phase, isLoading = true)
        repository.subscriptionPlans().valueOrNull()?.let { plans = it }
        (repository.mySubscription() as? CaptainResult.Success)?.let { subscription = it.value }
        _uiState.value = CaptainUiState.Plans(plans = plans, subscription = subscription, reason = reason, phase = phase, isLoading = false)
    }

    /** Back from a renewal: Home, blocked or not. Never from the first plan, never mid-payment. */
    fun dismissPlans() {
        val current = _uiState.value as? CaptainUiState.Plans ?: return
        if (!current.canGoBack) return
        viewModelScope.launch {
            showHome()
            refreshEarnings()
        }
    }

    /** One tap: the trial is granted by the server on the spot, once ever, and Home follows. */
    fun startTrial() {
        val current = _uiState.value as? CaptainUiState.Plans ?: return
        if (!current.trialAvailable) {
            _uiState.value = current.copy(errorMessage = TRIAL_USED)
            return
        }
        choosePlan(SubscriptionPlan.TRIAL_CODE, PaymentMethod.UPI)
    }

    /**
     * Checks [planCode] out. The trial answers `active` and goes to Home; a
     * paid plan answers the sheet's session, which the Activity opens. Nothing
     * here decides "paid": the sheet's ending leads to [confirm].
     */
    fun choosePlan(planCode: String, method: PaymentMethod) {
        val current = _uiState.value as? CaptainUiState.Plans ?: return
        when (current.phase) {
            PlanPhase.Choosing, is PlanPhase.Failed, is PlanPhase.StillConfirming -> Unit
            else -> return
        }
        if (planCode == SubscriptionPlan.TRIAL_CODE && !current.trialAvailable) {
            _uiState.value = current.copy(errorMessage = TRIAL_USED)
            return
        }
        setPlanPhase(PlanPhase.Starting(planCode))
        viewModelScope.launch {
            when (val result = repository.checkout(planCode, method)) {
                is CaptainResult.Failure -> setPlanPhase(PlanPhase.Failed(planCode, method, result.error.userMessage(), retryable = true))
                is CaptainResult.Success -> {
                    val checkout = result.value
                    if (checkout.isActive) {
                        activated(pause = false)
                        return@launch
                    }
                    val session = checkout.toPaymentSession(description = "Mopedu Captain · ${planName(planCode)}")
                    if (session == null) {
                        setPlanPhase(PlanPhase.Failed(planCode, method, "Online payment isn't available right now.", retryable = true))
                        return@launch
                    }
                    val attempt = PaymentAttempt(
                        applicationId = MOPEDU_PAYMENT_APPLICATION_ID,
                        referenceId = checkout.subscriptionId,
                        id = repository.newIdempotencyKey(),
                    )
                    saved.inFlight.attempt = attempt
                    saved.planCode = planCode
                    saved.paymentMethod = method
                    setPlanPhase(PlanPhase.OpeningSheet(planCode, CaptainPaymentRequest(attempt, session)))
                }
            }
        }
    }

    private fun planName(planCode: String): String = plans.firstOrNull { it.code == planCode }?.name?.ifBlank { null } ?: planCode

    private fun observeHandoff() {
        viewModelScope.launch {
            handoff.events(MOPEDU_PAYMENT_APPLICATION_ID).collect { event ->
                val mine = saved.inFlight.attempt
                // Not ours: the rider's fare in another process, or an earlier attempt at this plan.
                if (mine == null || event.attempt != mine) return@collect
                // Already acted on: a replay after rotation must not restart anything.
                if (handoff.isConsumed(mine)) return@collect
                handoff.consume(mine)
                when (event) {
                    // However the sheet ended — including the SDK's "success" — ask the server.
                    is PaymentHandoffEvent.SheetClosed -> confirm(mine.referenceId)
                    is PaymentHandoffEvent.Unavailable -> {
                        val planCode = saved.planCode
                        val method = saved.paymentMethod ?: PaymentMethod.UPI
                        saved.clear()
                        setPlanPhase(PlanPhase.Failed(planCode, method, event.reason, retryable = true))
                    }
                }
            }
        }
    }

    private fun confirm(subscriptionId: String) {
        confirmJob?.cancel()
        setPlanPhase(PlanPhase.Confirming(saved.planCode, elapsedSeconds = 0))
        confirmJob = viewModelScope.launch {
            payments.confirm(MOPEDU_PAYMENT_APPLICATION_ID, subscriptionId, statusSource, policy).collect { confirmation ->
                val planCode = saved.planCode
                val method = saved.paymentMethod ?: PaymentMethod.UPI
                when (confirmation) {
                    is PaymentConfirmation.Confirming -> setPlanPhase(PlanPhase.Confirming(planCode, confirmation.elapsedSeconds))
                    PaymentConfirmation.Paid -> activated(pause = true)
                    is PaymentConfirmation.Failed -> {
                        saved.clear()
                        setPlanPhase(PlanPhase.Failed(planCode, method, confirmation.reason, confirmation.retryable))
                    }
                    PaymentConfirmation.RefundPending, PaymentConfirmation.Refunded -> {
                        saved.clear()
                        setPlanPhase(PlanPhase.Failed(planCode, method, "The payment is being returned to you.", retryable = true))
                    }
                    is PaymentConfirmation.TimedOut -> setPlanPhase(PlanPhase.StillConfirming(planCode))
                }
            }
        }
    }

    /** Still confirming after a timeout: ask the server again. */
    fun checkPaymentAgain() {
        val current = _uiState.value as? CaptainUiState.Plans ?: return
        if (current.phase !is PlanPhase.StillConfirming) return
        val attempt = saved.inFlight.attempt ?: return
        confirm(attempt.referenceId)
    }

    /** The server said active (the trial) or paid (the status source). The only way to Home from the plans. */
    private suspend fun activated(pause: Boolean) {
        saved.clear()
        (repository.mySubscription() as? CaptainResult.Success)?.let { subscription = it.value }
        updatePlans { it.copy(subscription = subscription, phase = PlanPhase.Activated, errorMessage = null) }
        if (pause) delay(SETTLED_PAUSE_MILLIS)
        showHome()
        refreshEarnings()
    }

    /** A sheet was requested before the process died. Ask the server what happened; never reopen a sheet. */
    private fun resumePlanPayment(attempt: PaymentAttempt) {
        viewModelScope.launch {
            repository.profile().valueOrNull()?.let {
                profile = it.profile
                review = it.review
            }
            openPlansNow(reason = plansReasonNow(), phase = PlanPhase.Confirming(saved.planCode, elapsedSeconds = 0))
            confirm(attempt.referenceId)
        }
    }

    /** The plan payment in flight, if any. Exposed for the screen's abandon edge and for tests. */
    fun activeAttempt(): PaymentAttempt? = saved.inFlight.attempt

    private fun setPlanPhase(phase: PlanPhase) = updatePlans { it.copy(phase = phase, errorMessage = null) }

    private inline fun updatePlans(transform: (CaptainUiState.Plans) -> CaptainUiState.Plans) =
        _uiState.update { s -> if (s is CaptainUiState.Plans) transform(s) else s }

    // ── Pushes ──────────────────────────────────────────────────────────

    /**
     * A tapped push. The server is asked first so a link never lands on a
     * stale screen; a ride in progress is never interrupted.
     */
    private fun observeDeepLinks() {
        viewModelScope.launch {
            deepLinks.incoming.collect { link ->
                deepLinks.consume()
                if (_uiState.value.isRide) return@collect
                route()
                if (link == CaptainDeepLink.Plans && _uiState.value is CaptainUiState.Home) openPlansNow(plansReasonNow())
            }
        }
    }

    // ── Home: duty ──────────────────────────────────────────────────────

    private fun showHome(errorMessage: String? = null) {
        val previous = _uiState.value as? CaptainUiState.Home
        _uiState.value = CaptainUiState.Home(
            stats = stats,
            profile = profile,
            subscription = subscription,
            renewal = renewalNoticeFor(subscription, clock.nowMillis()),
            duty = flow.state,
            lastPingOk = previous?.lastPingOk,
            errorMessage = errorMessage,
        )
        if (flow.state == DutyState.Online) startOfferPolling()
    }

    private suspend fun refreshEarnings() {
        val earned = repository.earnings().valueOrNull() ?: return
        stats = earned.copy(rating = profile?.rating ?: earned.rating)
        updateHome { it.copy(stats = stats) }
    }

    /** From the banner or the blocking card: the plans, to renew. */
    fun renewPlan() {
        val home = _uiState.value as? CaptainUiState.Home ?: return
        openPlans(if (home.isBlocked) PlansReason.EXPIRED else PlansReason.RENEWAL)
    }

    fun onGoOnlineTapped(permissionGranted: Boolean) {
        val home = _uiState.value as? CaptainUiState.Home ?: return
        if (home.isBlocked) {
            updateHome { it.copy(errorMessage = PLAN_EXPIRED) }
            return
        }
        perform(flow.onGoOnlineTapped(permissionGranted, disclosure.accepted()))
    }

    fun onDisclosureAccepted(permissionGranted: Boolean) {
        disclosure.markAccepted()
        perform(flow.onDisclosureAccepted(permissionGranted))
    }

    fun onDisclosureDeclined() {
        flow.onDisclosureDeclined()
        publishDuty()
    }

    fun onPermissionResult(granted: Boolean, canAskAgain: Boolean) = perform(flow.onPermissionResult(granted, canAskAgain))

    fun onGoOfflineTapped() = perform(flow.onGoOfflineTapped())

    private fun perform(effect: DutyEffect) {
        publishDuty()
        when (effect) {
            DutyEffect.None -> Unit
            DutyEffect.RequestPermission -> _events.tryEmit(CaptainEvent.RequestLocationPermission)
            DutyEffect.SetServerOnline -> viewModelScope.launch {
                val result = repository.setOnline(true)
                if (result is CaptainResult.Failure) updateHome { it.copy(errorMessage = result.error.userMessage()) }
                perform(flow.onServerOnline(accepted = result is CaptainResult.Success))
            }
            DutyEffect.StartService -> {
                _events.tryEmit(CaptainEvent.StartLocationService)
                startOfferPolling()
            }
            DutyEffect.StopService -> {
                stopOfferPolling()
                duty.requestStop(OfflineReason.TOGGLED_OFF)
            }
        }
    }

    private fun onDutyStatus(status: DutyStatus) {
        when (status) {
            is DutyStatus.Online -> {
                if (flow.state !is DutyState.Online) flow.onServiceRunning()
                updateHome { it.copy(lastPingOk = status.lastPingOk) }
                startOfferPolling()
            }
            is DutyStatus.Offline -> {
                val wasOnDuty = flow.state == DutyState.Online || flow.state == DutyState.GoingOffline
                val reason = status.reason
                if (wasOnDuty && reason != null) {
                    flow.onServiceStopped(reason)
                    stopOfferPolling()
                    if (reason != OfflineReason.TOGGLED_OFF) updateHome { it.copy(errorMessage = reason.explanation()) }
                }
            }
        }
        publishDuty()
    }

    private fun publishDuty() = updateHome { it.copy(duty = flow.state) }

    // ── Home: offers ────────────────────────────────────────────────────

    private fun startOfferPolling() {
        if (offerJob?.isActive == true) return
        offerJob = viewModelScope.launch {
            while (isActive) {
                val home = _uiState.value as? CaptainUiState.Home
                if (home != null && home.isOnline && home.incomingOffer == null) {
                    repository.incomingOffers().valueOrNull()
                        ?.firstOrNull { !it.isExpiredAt(clock.nowMillis()) }
                        ?.let { offer ->
                            updateHome { it.copy(incomingOffer = offer, offerSecondsLeft = offer.secondsLeftAt(clock.nowMillis())) }
                            startCountdown(offer)
                        }
                }
                delay(OFFER_POLL_MILLIS)
            }
        }
    }

    private fun stopOfferPolling() {
        offerJob?.cancel()
        offerJob = null
        countdownJob?.cancel()
        updateHome { it.copy(incomingOffer = null, offerSecondsLeft = 0) }
    }

    private fun startCountdown(offer: CaptainOffer) {
        countdownJob?.cancel()
        countdownJob = viewModelScope.launch {
            while (isActive) {
                delay(COUNTDOWN_MILLIS)
                val home = _uiState.value as? CaptainUiState.Home ?: return@launch
                if (home.incomingOffer?.id != offer.id) return@launch
                val left = offer.secondsLeftAt(clock.nowMillis())
                if (left <= 0L) {
                    updateHome { it.copy(incomingOffer = null, offerSecondsLeft = 0) }
                    return@launch
                }
                updateHome { it.copy(offerSecondsLeft = left) }
            }
        }
    }

    fun acceptOffer() {
        val home = _uiState.value as? CaptainUiState.Home ?: return
        val offer = home.incomingOffer ?: return
        if (home.isAccepting) return
        updateHome { it.copy(isAccepting = true, errorMessage = null) }
        viewModelScope.launch {
            when (val result = repository.acceptOffer(offer.id)) {
                is CaptainResult.Success -> {
                    countdownJob?.cancel()
                    offerJob?.cancel()
                    duty.setOnRide(true)
                    _uiState.value = CaptainUiState.EnRouteToPickup(booking = offer.toBooking(result.value))
                    viewModelScope.launch { repository.markArriving(result.value) }
                }
                is CaptainResult.Failure -> {
                    updateHome { it.copy(isAccepting = false, incomingOffer = null, offerSecondsLeft = 0, errorMessage = result.error.userMessage()) }
                }
            }
        }
    }

    fun rejectOffer() {
        val home = _uiState.value as? CaptainUiState.Home ?: return
        val offer = home.incomingOffer ?: return
        countdownJob?.cancel()
        updateHome { it.copy(incomingOffer = null, offerSecondsLeft = 0) }
        viewModelScope.launch { repository.rejectOffer(offer.id) }
    }

    // ── The ride ────────────────────────────────────────────────────────

    fun markArrived() {
        val state = _uiState.value as? CaptainUiState.EnRouteToPickup ?: return
        if (state.isArriving) return
        _uiState.value = state.copy(isArriving = true, errorMessage = null)
        viewModelScope.launch {
            when (val result = repository.markArrived(state.booking.id)) {
                is CaptainResult.Success -> _uiState.value = CaptainUiState.VerifyOtp(booking = state.booking.copy(status = RideStatus.ARRIVED))
                is CaptainResult.Failure -> _uiState.value = state.copy(isArriving = false, errorMessage = result.error.userMessage())
            }
        }
    }

    fun onOtpInputChanged(otp: String) {
        val state = _uiState.value as? CaptainUiState.VerifyOtp ?: return
        _uiState.value = state.copy(otpInput = otp.filter { it.isDigit() }.take(OTP_LENGTH), errorMessage = null)
    }

    fun verifyOtpAndStart() {
        val state = _uiState.value as? CaptainUiState.VerifyOtp ?: return
        if (state.isVerifying) return
        if (state.otpInput.length < OTP_LENGTH) {
            _uiState.value = state.copy(errorMessage = "Enter the customer's 4-digit code.")
            return
        }
        _uiState.value = state.copy(isVerifying = true, errorMessage = null)
        viewModelScope.launch {
            when (val result = repository.verifyOtpAndStart(state.booking.id, state.otpInput)) {
                is CaptainResult.Success -> _uiState.value = CaptainUiState.TripInProgress(booking = state.booking.copy(status = RideStatus.IN_PROGRESS))
                is CaptainResult.Failure -> _uiState.value = state.copy(isVerifying = false, errorMessage = "That code didn't match. Check it with the customer.")
            }
        }
    }

    fun completeTrip(finalDistanceKm: Double = 0.0, finalDurationMin: Int = 0) {
        val state = _uiState.value as? CaptainUiState.TripInProgress ?: return
        if (state.isCompleting) return
        _uiState.value = state.copy(isCompleting = true, errorMessage = null)
        viewModelScope.launch {
            when (val result = repository.completeRide(state.booking.id, finalDistanceKm, finalDurationMin, repository.newIdempotencyKey())) {
                is CaptainResult.Success -> {
                    duty.setOnRide(false)
                    val booking = state.booking.copy(status = RideStatus.COMPLETED)
                    val amount = booking.finalFare ?: booking.estimatedFare
                    val phase = if (booking.paymentMethod.isOnline) {
                        CollectPhase.WaitingForCustomer(booking.paymentMethod, amount)
                    } else {
                        CollectPhase.Cash(amount)
                    }
                    _uiState.value = CaptainUiState.CollectPayment(booking = booking, phase = phase)
                    watchPayment(booking.id)
                }
                is CaptainResult.Failure -> _uiState.value = state.copy(isCompleting = false, errorMessage = result.error.userMessage())
            }
        }
    }

    // ── Collecting the fare ─────────────────────────────────────────────

    fun confirmCashPayment() {
        val state = _uiState.value as? CaptainUiState.CollectPayment ?: return
        val cash = state.phase as? CollectPhase.Cash ?: return
        if (cash.isConfirming || cash.confirmed) return
        _uiState.value = state.copy(phase = cash.copy(isConfirming = true), errorMessage = null)
        viewModelScope.launch {
            when (val result = repository.confirmCashPayment(state.booking.id)) {
                is CaptainResult.Success -> {
                    paymentJob?.cancel()
                    _uiState.value = state.copy(phase = cash.copy(isConfirming = false, confirmed = true))
                    delay(SETTLED_PAUSE_MILLIS)
                    finishRide()
                }
                is CaptainResult.Failure -> _uiState.value = state.copy(phase = cash.copy(isConfirming = false), errorMessage = result.error.userMessage())
            }
        }
    }

    /**
     * Polls `GET /rides/{id}/payment` until the SERVER settles it. Cash confirmed
     * by the customer's side (a switch, an admin) settles too; the captain's own
     * tap is [confirmCashPayment].
     */
    private fun watchPayment(rideId: String) {
        paymentJob?.cancel()
        paymentJob = viewModelScope.launch {
            while (isActive) {
                delay(PAYMENT_POLL_MILLIS)
                val state = _uiState.value as? CaptainUiState.CollectPayment ?: return@launch
                val payment = repository.ridePayment(rideId).valueOrNull() ?: continue
                val amount = payment.amount.takeIf { !it.isZero } ?: state.booking.finalFare ?: state.booking.estimatedFare
                val phase: CollectPhase? = when (payment.status) {
                    RidePaymentStatus.PAID -> CollectPhase.OnlinePaid(payment.method, amount)
                    RidePaymentStatus.CASH_CONFIRMED -> CollectPhase.Cash(amount, confirmed = true)
                    RidePaymentStatus.FAILED -> CollectPhase.OnlineFailed(payment.method, amount)
                    RidePaymentStatus.REFUNDED, RidePaymentStatus.PARTIALLY_REFUNDED -> CollectPhase.Refunding
                    // The customer switched to cash: the captain collects it.
                    RidePaymentStatus.CASH_PENDING -> (state.phase as? CollectPhase.Cash) ?: CollectPhase.Cash(amount)
                    // An online payment still pending, or a status newer than this build: keep waiting.
                    RidePaymentStatus.CONFIRMING, RidePaymentStatus.UNKNOWN ->
                        if (payment.method.isOnline && state.phase !is CollectPhase.WaitingForCustomer) {
                            CollectPhase.WaitingForCustomer(payment.method, amount)
                        } else {
                            null
                        }
                }
                if (phase != null && phase != state.phase) _uiState.value = state.copy(phase = phase)
                if (phase is CollectPhase.OnlinePaid || (phase is CollectPhase.Cash && phase.confirmed)) {
                    delay(SETTLED_PAUSE_MILLIS)
                    finishRide()
                    return@launch
                }
            }
        }
    }

    /** The captain leaves a failed or refunded payment to the customer and goes back to Home. */
    fun finishRide() {
        paymentJob?.cancel()
        viewModelScope.launch {
            showHome()
            refreshEarnings()
        }
    }

    fun dismissError() = _uiState.update { s ->
        when (s) {
            is CaptainUiState.Home -> s.copy(errorMessage = null)
            is CaptainUiState.EnRouteToPickup -> s.copy(errorMessage = null)
            is CaptainUiState.VerifyOtp -> s.copy(errorMessage = null)
            is CaptainUiState.TripInProgress -> s.copy(errorMessage = null)
            is CaptainUiState.CollectPayment -> s.copy(errorMessage = null)
            is CaptainUiState.Onboarding -> s.copy(errorMessage = null)
            is CaptainUiState.Plans -> s.copy(errorMessage = null)
            CaptainUiState.Loading -> s
        }
    }

    private inline fun updateHome(transform: (CaptainUiState.Home) -> CaptainUiState.Home) =
        _uiState.update { s -> if (s is CaptainUiState.Home) transform(s) else s }

    private val CaptainUiState.isRide: Boolean
        get() = this is CaptainUiState.EnRouteToPickup || this is CaptainUiState.VerifyOtp ||
            this is CaptainUiState.TripInProgress || this is CaptainUiState.CollectPayment

    override fun onCleared() {
        offerJob?.cancel()
        countdownJob?.cancel()
        paymentJob?.cancel()
        verifyJob?.cancel()
        confirmJob?.cancel()
        super.onCleared()
    }

    private companion object {
        const val DRAFT = "draft"
        const val KYC_PENDING = "pending"
        const val DOCUMENT_SELFIE = "selfie"
        const val OTP_LENGTH = 4
        const val OFFER_POLL_MILLIS = 3_000L
        const val COUNTDOWN_MILLIS = 1_000L
        const val PAYMENT_POLL_MILLIS = 3_000L
        const val SETTLED_PAUSE_MILLIS = 1_500L
        const val VERIFY_POLL_MILLIS = 3_000L
        const val VERIFY_MAX_POLLS = 10
        const val TRIAL_USED = "The free trial can be used once, and this account has already had it."
        const val PLAN_EXPIRED = "Your plan has run out. Renew it to go online."
    }
}

/**
 * What must survive process death: the plan payment's attempt (in
 * `:core:payments`' record keyed by Mopedu's application id) and which plan,
 * by which method, it was for. Never a provider session.
 */
internal class CaptainContinuation(private val handle: SavedStateHandle) {

    var planCode: String?
        get() = handle[KEY_PLAN_CODE]
        set(v) {
            handle[KEY_PLAN_CODE] = v
        }

    var paymentMethod: PaymentMethod?
        get() = handle.get<String>(KEY_PAYMENT_METHOD)?.let { PaymentMethod.fromCode(it) }
        set(v) {
            handle[KEY_PAYMENT_METHOD] = v?.code
        }

    val inFlight = InFlightPayment(
        store = object : PaymentStateStore {
            override fun get(key: String): String? = handle[key]
            override fun set(key: String, value: String?) {
                handle[key] = value
            }
        },
        applicationId = MOPEDU_PAYMENT_APPLICATION_ID,
    )

    fun clear() {
        inFlight.clear()
        planCode = null
        paymentMethod = null
    }

    private companion object {
        const val KEY_PLAN_CODE = "captain.plan.code"
        const val KEY_PAYMENT_METHOD = "captain.plan.method"
    }
}

/** The accepted offer as the ride the captain drives. The server's ride carries the rest. */
internal fun CaptainOffer.toBooking(rideId: String): RideBooking = RideBooking(
    id = rideId,
    customerUserId = "",
    partnerId = null,
    vehicleId = null,
    quoteId = null,
    revision = 1,
    vehicleType = vehicleType,
    status = RideStatus.PARTNER_ASSIGNED,
    pickup = pickup,
    drop = drop,
    estimatedFare = estimatedEarnings,
    finalFare = null,
    paymentMethod = paymentMethod,
    otp = null,
    requestedAtEpochMs = 0L,
)

/** What Home says when the service went offline on its own. */
fun OfflineReason.explanation(): String = when (this) {
    OfflineReason.TOGGLED_OFF -> "You went offline."
    OfflineReason.SIGNED_OUT -> "You were signed out, so you went offline."
    OfflineReason.PERMISSION_REVOKED -> "Location access was turned off, so you went offline."
    OfflineReason.NO_FIX -> "We lost your location for two minutes, so you went offline."
    OfflineReason.SERVER_REFUSED -> "Mopedu couldn't put you online just now."
}
