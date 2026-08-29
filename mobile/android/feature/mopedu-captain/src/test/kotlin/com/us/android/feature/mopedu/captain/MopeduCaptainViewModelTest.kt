package com.us.android.feature.mopedu.captain

import com.google.common.truth.Truth.assertThat
import com.us.android.core.location.LocationTracker
import com.us.android.core.mobility.model.CaptainOffer
import com.us.android.core.mobility.model.CaptainState
import com.us.android.core.mobility.model.CaptainTelemetry
import com.us.android.core.mobility.model.GeoPoint
import com.us.android.core.mobility.model.MoneyPaise
import com.us.android.core.mobility.model.PartnerDocument
import com.us.android.core.mobility.model.PartnerProfile
import com.us.android.core.mobility.model.PartnerSubscription
import com.us.android.core.mobility.model.RideStatus
import com.us.android.core.mobility.model.SubscriptionPlan
import com.us.android.core.mobility.model.Vehicle
import com.us.android.core.mobility.model.VehicleType
import com.us.android.feature.mopedu.captain.data.AadhaarStartResponseDto
import com.us.android.feature.mopedu.captain.data.MopeduCaptainRepository
import com.us.android.feature.mopedu.captain.data.SubscribeResponseDto
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Before
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class MopeduCaptainViewModelTest {

    private val testDispatcher = StandardTestDispatcher()
    private val fakeLocationTracker = FakeLocationTracker()
    private val fakeRepo = FakeCaptainRepository()

    @Before
    fun setUp() {
        Dispatchers.setMain(testDispatcher)
    }

    @After
    fun tearDown() {
        Dispatchers.resetMain()
    }

    @Test
    fun initialCheck_whenProfileNotApproved_opensOnboarding() = runTest(testDispatcher) {
        fakeRepo.profileResult = Result.success(
            PartnerProfile(
                id = "p-1",
                partnerType = "individual_driver",
                fullName = "Rahul",
                phone = "+919876543210",
                status = "draft",
                kycStatus = "pending",
            )
        )

        val vm = MopeduCaptainViewModel(fakeLocationTracker, fakeRepo)
        advanceUntilIdle()

        val state = vm.uiState.value
        assertThat(state).isInstanceOf(CaptainUiState.Onboarding::class.java)
        val onboarding = state as CaptainUiState.Onboarding
        assertThat(onboarding.step).isEqualTo(OnboardingStep.VEHICLE)
    }

    @Test
    fun initialCheck_whenProfileAndSubApproved_opensOfflineConsole() = runTest(testDispatcher) {
        fakeRepo.profileResult = Result.success(
            PartnerProfile(
                id = "p-1",
                partnerType = "individual_driver",
                fullName = "Rahul",
                phone = "+919876543210",
                status = "approved",
                kycStatus = "approved",
            )
        )
        fakeRepo.subscriptionResult = Result.success(
            PartnerSubscription(
                id = "sub-1",
                partnerId = "p-1",
                planId = "plan-1",
                planCode = "trial_7d",
                planName = "Trial",
                status = "trial",
                leadsUsed = 0,
                dailyLeadCap = 10,
                startsAt = "2026-08-28T00:00:00Z",
                expiresAt = "2026-09-04T00:00:00Z",
            )
        )

        val vm = MopeduCaptainViewModel(fakeLocationTracker, fakeRepo)
        advanceUntilIdle()

        val state = vm.uiState.value
        assertThat(state).isInstanceOf(CaptainUiState.Offline::class.java)
    }

    @Test
    fun toggleOnline_success_transitionsToOnlineIdle() = runTest(testDispatcher) {
        val vm = MopeduCaptainViewModel(fakeLocationTracker, fakeRepo)
        advanceUntilIdle()

        vm.toggleOnline()
        advanceUntilIdle()

        val state = vm.uiState.value
        assertThat(state).isInstanceOf(CaptainUiState.OnlineIdle::class.java)
        val onlineState = state as CaptainUiState.OnlineIdle
        assertThat(onlineState.captainState.isOnline).isTrue()
    }

    @Test
    fun acceptOffer_transitionsToEnRouteToPickup() = runTest(testDispatcher) {
        val vm = MopeduCaptainViewModel(fakeLocationTracker, fakeRepo)
        advanceUntilIdle()
        vm.toggleOnline()
        advanceUntilIdle()

        val offer = CaptainOffer(
            id = "offer-1",
            rideId = "ride-123",
            pickup = GeoPoint(12.9716, 77.5946, "MG Road"),
            drop = GeoPoint(12.9784, 77.6408, "Indiranagar"),
            distanceKM = 4.5,
            estimatedEarnings = MoneyPaise(9500L),
            score = 98.0,
            expiresAtEpochMs = System.currentTimeMillis() + 15000L,
        )

        vm.acceptOffer(offer)
        advanceUntilIdle()

        val state = vm.uiState.value
        assertThat(state).isInstanceOf(CaptainUiState.EnRouteToPickup::class.java)
        val enRoute = state as CaptainUiState.EnRouteToPickup
        assertThat(enRoute.booking.id).isEqualTo("ride-123")
    }

    @Test
    fun markArrived_and_verifyOtp_startsTrip() = runTest(testDispatcher) {
        val vm = MopeduCaptainViewModel(fakeLocationTracker, fakeRepo)
        advanceUntilIdle()
        vm.toggleOnline()
        advanceUntilIdle()

        val offer = CaptainOffer(
            id = "offer-1",
            rideId = "ride-123",
            pickup = GeoPoint(12.9716, 77.5946, "MG Road"),
            drop = GeoPoint(12.9784, 77.6408, "Indiranagar"),
            distanceKM = 4.5,
            estimatedEarnings = MoneyPaise(9500L),
            score = 98.0,
            expiresAtEpochMs = System.currentTimeMillis() + 15000L,
        )
        vm.acceptOffer(offer)
        advanceUntilIdle()

        vm.markArrived()
        advanceUntilIdle()

        assertThat(vm.uiState.value).isInstanceOf(CaptainUiState.VerifyOtp::class.java)

        vm.onOtpInputChanged("1234")
        vm.verifyOtpAndStart()
        advanceUntilIdle()

        val state = vm.uiState.value
        assertThat(state).isInstanceOf(CaptainUiState.TripInProgress::class.java)
        val inProgress = state as CaptainUiState.TripInProgress
        assertThat(inProgress.booking.status).isEqualTo(RideStatus.IN_PROGRESS)
    }

    @Test
    fun completeTrip_and_confirmCash_settlesRide() = runTest(testDispatcher) {
        val vm = MopeduCaptainViewModel(fakeLocationTracker, fakeRepo)
        advanceUntilIdle()
        vm.toggleOnline()
        advanceUntilIdle()

        val offer = CaptainOffer(
            id = "offer-1",
            rideId = "ride-123",
            pickup = GeoPoint(12.9716, 77.5946, "MG Road"),
            drop = GeoPoint(12.9784, 77.6408, "Indiranagar"),
            distanceKM = 4.5,
            estimatedEarnings = MoneyPaise(9500L),
            score = 98.0,
            expiresAtEpochMs = System.currentTimeMillis() + 15000L,
        )
        vm.acceptOffer(offer)
        advanceUntilIdle()
        vm.markArrived()
        advanceUntilIdle()
        vm.onOtpInputChanged("1234")
        vm.verifyOtpAndStart()
        advanceUntilIdle()

        vm.completeTrip()
        advanceUntilIdle()

        assertThat(vm.uiState.value).isInstanceOf(CaptainUiState.CollectPayment::class.java)

        vm.confirmCashPayment()
        advanceUntilIdle()

        val state = vm.uiState.value
        assertThat(state).isInstanceOf(CaptainUiState.OnlineIdle::class.java)
    }

    // --- Fake Test Doubles ---

    private class FakeLocationTracker : LocationTracker {
        private val _point = MutableStateFlow(GeoPoint(12.9716, 77.5946, "Bengaluru"))
        override val locationFlow: StateFlow<GeoPoint> = _point
        private val _telemetry = MutableStateFlow(
            CaptainTelemetry(12.9716, 77.5946, 5.0, 90.0, 4.0, 1L, System.currentTimeMillis())
        )
        override val telemetryFlow: StateFlow<CaptainTelemetry> = _telemetry

        override fun updateLocation(lat: Double, lng: Double, address: String) {
            _point.value = GeoPoint(lat, lng, address)
        }
    }

    private class FakeCaptainRepository : MopeduCaptainRepository {
        var profileResult: Result<PartnerProfile> = Result.failure(Exception("Not found"))
        var subscriptionResult: Result<PartnerSubscription?> = Result.success(null)

        override suspend fun setOnline(online: Boolean): Result<Unit> = Result.success(Unit)
        override suspend fun sendLocation(telemetry: CaptainTelemetry): Result<Unit> = Result.success(Unit)
        override suspend fun getIncomingOffers(): Result<List<CaptainOffer>> = Result.success(emptyList())
        override suspend fun acceptOffer(offerId: String): Result<String> = Result.success("ride-123")
        override suspend fun rejectOffer(offerId: String): Result<Unit> = Result.success(Unit)
        override suspend fun markArriving(rideId: String): Result<Unit> = Result.success(Unit)
        override suspend fun markArrived(rideId: String): Result<Unit> = Result.success(Unit)
        override suspend fun verifyOtpAndStart(rideId: String, otp: String): Result<Unit> = Result.success(Unit)
        override suspend fun completeRide(rideId: String, finalDistanceKm: Double, finalDurationMin: Int): Result<Unit> = Result.success(Unit)
        override suspend fun confirmCashPayment(rideId: String): Result<Unit> = Result.success(Unit)
        override suspend fun getEarnings(): Result<CaptainState> = Result.success(
            CaptainState(isOnline = true, activeRideId = null, rating = 4.9, totalRidesCompleted = 5, todayEarnings = MoneyPaise(45000L))
        )

        override suspend fun getProfile(): Result<PartnerProfile> = profileResult
        override suspend fun createProfile(fullName: String, phone: String, email: String?, cityId: String?): Result<PartnerProfile> =
            Result.success(PartnerProfile("p-1", "individual_driver", fullName, phone, email, "draft", "pending", cityId))

        override suspend fun updateProfile(fullName: String?, email: String?, profilePhotoUrl: String?, cityId: String?): Result<PartnerProfile> =
            Result.success(PartnerProfile("p-1", "individual_driver", fullName ?: "R", "+919876543210", email, "draft", "pending", cityId))

        override suspend fun getDocuments(): Result<List<PartnerDocument>> = Result.success(emptyList())
        override suspend fun submitDocument(documentType: String, documentNumber: String?, fileUrl: String, expiresAt: String?): Result<PartnerDocument> =
            Result.success(PartnerDocument("doc-1", "p-1", documentType, documentNumber, fileUrl, "submitted"))

        override suspend fun startAadhaar(): Result<AadhaarStartResponseDto> =
            Result.success(AadhaarStartResponseDto("https://digilocker.gov.in/auth", "req-1"))

        override suspend fun callbackAadhaar(requestId: String, assertionToken: String): Result<PartnerProfile> =
            Result.success(PartnerProfile("p-1", "individual_driver", "Rahul", "+919876543210", null, "approved", "approved"))

        override suspend fun getVehicles(): Result<List<Vehicle>> = Result.success(emptyList())
        override suspend fun addVehicle(vehicleType: VehicleType, registrationNumber: String, brand: String?, model: String?, color: String?, year: Int?): Result<Vehicle> =
            Result.success(Vehicle("v-1", "p-1", vehicleType, registrationNumber, brand, model, color, year, "approved"))

        override suspend fun submitVehicleDocument(vehicleId: String, documentType: String, fileUrl: String): Result<PartnerDocument> =
            Result.success(PartnerDocument("doc-v-1", "p-1", documentType, null, fileUrl, "submitted"))

        override suspend fun getSubscriptionPlans(): Result<List<SubscriptionPlan>> = Result.success(
            listOf(
                SubscriptionPlan("plan-1", "trial_7d", "7-Day Free Trial", "bike", "trial", MoneyPaise(0L), 10, 10, "Free trial"),
                SubscriptionPlan("plan-2", "daily_bike", "Daily Bike Plan", "bike", "daily", MoneyPaise(1900L), 15, 10, "₹19/day"),
            )
        )

        override suspend fun subscribe(planId: String, paymentMethod: String): Result<SubscribeResponseDto> =
            Result.success(SubscribeResponseDto("sub-1", "pay-1", "active", null, "2026-08-28T00:00:00Z", "2026-09-04T00:00:00Z"))

        override suspend fun submitPaymentProof(paymentId: String, fileUrl: String): Result<Unit> = Result.success(Unit)
        override suspend fun getMySubscription(): Result<PartnerSubscription?> = subscriptionResult
    }
}
