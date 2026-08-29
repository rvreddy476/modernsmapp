package com.us.android.feature.mopedu.rider

import com.google.common.truth.Truth.assertThat
import com.us.android.core.mobility.model.CaptainInfo
import com.us.android.core.mobility.model.GeoPoint
import com.us.android.core.mobility.model.MoneyPaise
import com.us.android.core.mobility.model.QuoteBreakdown
import com.us.android.core.mobility.model.QuoteOption
import com.us.android.core.mobility.model.QuoteSnapshot
import com.us.android.core.mobility.model.RideBooking
import com.us.android.core.mobility.model.RideReceipt
import com.us.android.core.mobility.model.RideStatus
import com.us.android.core.mobility.model.VehicleType
import com.us.android.feature.mopedu.rider.data.MopeduRiderRepository
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Before
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class MopeduRiderViewModelTest {

    private val testDispatcher = StandardTestDispatcher()
    private val fakeRepo = FakeRiderRepository()

    @Before
    fun setUp() {
        Dispatchers.setMain(testDispatcher)
    }

    @After
    fun tearDown() {
        Dispatchers.resetMain()
    }

    @Test
    fun initialState_isLocationSelect() = runTest(testDispatcher) {
        val vm = MopeduRiderViewModel(fakeRepo)
        advanceUntilIdle()

        val state = vm.uiState.value
        assertThat(state).isInstanceOf(RiderUiState.LocationSelect::class.java)
    }

    @Test
    fun requestQuote_success_transitionsToQuoteSelect() = runTest(testDispatcher) {
        val vm = MopeduRiderViewModel(fakeRepo)
        advanceUntilIdle()

        vm.requestQuote()
        advanceUntilIdle()

        val state = vm.uiState.value
        assertThat(state).isInstanceOf(RiderUiState.QuoteSelect::class.java)
        val quoteSelect = state as RiderUiState.QuoteSelect
        assertThat(quoteSelect.quote.options).isNotEmpty()
        assertThat(quoteSelect.selectedOption.vehicleType).isEqualTo(VehicleType.BIKE)
    }

    @Test
    fun confirmBooking_success_transitionsToSearchingCaptain() = runTest(testDispatcher) {
        val vm = MopeduRiderViewModel(fakeRepo)
        advanceUntilIdle()

        vm.requestQuote()
        advanceUntilIdle()

        vm.confirmBooking()
        advanceUntilIdle()

        val state = vm.uiState.value
        assertThat(state).isInstanceOf(RiderUiState.SearchingCaptain::class.java)
        val searching = state as RiderUiState.SearchingCaptain
        assertThat(searching.booking.id).isEqualTo("ride-456")
    }

    @Test
    fun triggerSOS_callsRepoAndSetsFlag() = runTest(testDispatcher) {
        val vm = MopeduRiderViewModel(fakeRepo)
        advanceUntilIdle()

        vm.requestQuote()
        advanceUntilIdle()
        vm.confirmBooking()
        advanceUntilIdle()

        fakeRepo.activeRide = fakeRepo.sampleBooking.copy(status = RideStatus.IN_PROGRESS)
        vm.checkActiveRide()
        advanceUntilIdle()

        vm.triggerSOS()
        advanceUntilIdle()

        val state = vm.uiState.value
        assertThat(state).isInstanceOf(RiderUiState.TripInProgress::class.java)
        val inProgress = state as RiderUiState.TripInProgress
        assertThat(inProgress.sosTriggered).isTrue()
    }

    @Test
    fun submitRating_callsRepoAndUpdatesState() = runTest(testDispatcher) {
        val vm = MopeduRiderViewModel(fakeRepo)
        advanceUntilIdle()

        fakeRepo.activeRide = fakeRepo.sampleBooking.copy(status = RideStatus.COMPLETED)
        vm.checkActiveRide()
        advanceUntilIdle()

        assertThat(vm.uiState.value).isInstanceOf(RiderUiState.TripCompleted::class.java)

        vm.submitRating(5, "Great ride!")
        advanceUntilIdle()

        val state = vm.uiState.value as RiderUiState.TripCompleted
        assertThat(state.ratingSubmitted).isTrue()
    }

    // --- Fake Test Double ---

    private class FakeRiderRepository : MopeduRiderRepository {
        var activeRide: RideBooking? = null

        val sampleBooking = RideBooking(
            id = "ride-456",
            customerUserId = "user-123",
            partnerId = "p-1",
            vehicleId = "v-1",
            quoteId = "quote-1",
            revision = 1,
            vehicleType = VehicleType.BIKE,
            status = RideStatus.SEARCHING_PARTNER,
            pickup = GeoPoint(12.9716, 77.5946, "MG Road"),
            drop = GeoPoint(12.9784, 77.6408, "Indiranagar"),
            estimatedFare = MoneyPaise(6500L),
            finalFare = null,
            paymentMethod = "cash",
            otp = "4321",
            captain = CaptainInfo("c-1", "Ramesh", "+919876543210", 4.9, "Pulsar 150", "KA01AB1234", "Black"),
            requestedAtEpochMs = System.currentTimeMillis(),
            completedAtEpochMs = null,
        )

        override suspend fun getQuote(
            pickup: GeoPoint,
            drop: GeoPoint,
        ): Result<QuoteSnapshot> {
            val bikeOpt = QuoteOption(
                vehicleType = VehicleType.BIKE,
                available = true,
                pickupETASeconds = 180,
                distanceMeters = 5000,
                durationSeconds = 600,
                totalFare = MoneyPaise(6500L),
                breakdown = QuoteBreakdown(3000L, 2500L, 500L, 0L, 500L, 0L, 0L),
            )
            val autoOpt = QuoteOption(
                vehicleType = VehicleType.AUTO,
                available = true,
                pickupETASeconds = 240,
                distanceMeters = 5000,
                durationSeconds = 600,
                totalFare = MoneyPaise(9500L),
                breakdown = QuoteBreakdown(4500L, 3500L, 1000L, 0L, 500L, 0L, 0L),
            )
            return Result.success(
                QuoteSnapshot(
                    quoteId = "quote-1",
                    pickup = pickup,
                    drop = drop,
                    distanceMeters = 5000,
                    durationSeconds = 600,
                    options = listOf(bikeOpt, autoOpt),
                    expiresAtEpochMs = System.currentTimeMillis() + 120_000L,
                )
            )
        }

        override suspend fun bookRide(
            quoteId: String,
            pickup: GeoPoint,
            drop: GeoPoint,
            vehicleType: VehicleType,
        ): Result<RideBooking> = Result.success(sampleBooking)

        override suspend fun getActiveRide(): Result<RideBooking?> = Result.success(activeRide)

        override suspend fun triggerSOS(rideId: String, lat: Double, lng: Double): Result<Unit> =
            Result.success(Unit)

        override suspend fun createShareLink(rideId: String): Result<String> =
            Result.success("https://track.atpost.us/mopedu/share/abc123token")

        override suspend fun rateRide(rideId: String, rating: Int, feedback: String): Result<Unit> =
            Result.success(Unit)

        override suspend fun getReceipt(rideId: String): Result<RideReceipt> = Result.success(
            RideReceipt(
                rideId = rideId,
                customerUserId = "user-123",
                partnerId = "p-1",
                vehicleType = VehicleType.BIKE,
                status = "completed",
                pickupAddress = "MG Road",
                dropAddress = "Indiranagar",
                distanceMeters = 5000,
                durationSeconds = 600,
                totalFare = MoneyPaise(6500L),
                paymentMethod = "cash",
                paymentStatus = "succeeded",
                completedAtEpochMs = System.currentTimeMillis(),
            )
        )
    }
}
