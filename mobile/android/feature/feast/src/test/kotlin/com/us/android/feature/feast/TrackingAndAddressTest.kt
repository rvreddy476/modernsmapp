package com.us.android.feature.feast

import com.google.common.truth.Truth.assertThat
import com.us.android.core.food.network.FeastOrderDto
import com.us.android.core.realtime.RealtimeEvent
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.feast.address.AddAddressViewModel
import com.us.android.feature.feast.address.AddressLookup
import com.us.android.feature.feast.address.Coordinates
import com.us.android.feature.feast.address.CurrentLocationSource
import com.us.android.feature.feast.address.LocationEffect
import com.us.android.feature.feast.address.LocationStep
import com.us.android.feature.feast.address.LookedUpAddress
import com.us.android.feature.feast.tracking.DeliveryCodeRule
import com.us.android.feature.feast.tracking.RiderPosition
import com.us.android.feature.feast.tracking.TrackingFrames
import com.us.android.feature.feast.tracking.TrackingModel
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Rule
import org.junit.Test
import java.time.Instant

@OptIn(ExperimentalCoroutinesApi::class)
class TrackingAndAddressTest {

    private val dispatcher = StandardTestDispatcher()

    @get:Rule
    val main = MainDispatcherRule(dispatcher)

    // ── rider.location frames ───────────────────────────────────────────

    private val orderId = "0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0015"
    private val received = Instant.parse("2026-09-13T06:40:00Z")

    private fun frame(
        recordedAt: String,
        lat: Double = 12.9,
        eta: String? = null,
        order: String = orderId,
        type: String = "rider.location",
    ) = RealtimeEvent.Message(
        id = "1-0",
        topic = "food.order.$order",
        eventType = type,
        data = buildJsonObject {
            put("order_id", order)
            put("lat", lat)
            put("lng", 77.6)
            put("heading", 90.0)
            put("recorded_at", recordedAt)
            if (eta != null) {
                put("eta_at", eta)
                put("eta_source", "google")
            }
        },
        emittedAt = recordedAt,
    )

    @Test
    fun `a rider location frame moves the rider and sets the eta`() {
        val parsed = TrackingFrames.parse(orderId, frame("2026-09-13T06:39:55.123Z", eta = "2026-09-13T06:52:00Z"))!!

        val model = TrackingFrames.apply(TrackingModel(), parsed, received)

        assertThat(model.rider).isEqualTo(RiderPosition(12.9, 77.6, 90.0, Instant.parse("2026-09-13T06:39:55.123Z")))
        assertThat(model.etaAt).isEqualTo(Instant.parse("2026-09-13T06:52:00Z"))
        assertThat(model.etaSource).isEqualTo("google")
        assertThat(model.lastUpdatedAt).isEqualTo(received)
    }

    @Test
    fun `a stale or duplicate frame is ignored`() {
        val newer = TrackingFrames.parse(orderId, frame("2026-09-13T06:39:55Z", lat = 13.0, eta = "2026-09-13T06:50:00Z"))!!
        val model = TrackingFrames.apply(TrackingModel(), newer, received)

        val older = TrackingFrames.parse(orderId, frame("2026-09-13T06:39:40Z", lat = 12.0, eta = "2026-09-13T07:10:00Z"))!!
        val duplicate = TrackingFrames.parse(orderId, frame("2026-09-13T06:39:55Z", lat = 11.0))!!

        assertThat(TrackingFrames.apply(model, older, received.plusSeconds(5))).isEqualTo(model)
        assertThat(TrackingFrames.apply(model, duplicate, received.plusSeconds(5))).isEqualTo(model)
    }

    @Test
    fun `a newer frame without an eta keeps the last eta`() {
        val first = TrackingFrames.apply(
            TrackingModel(),
            TrackingFrames.parse(orderId, frame("2026-09-13T06:39:00Z", eta = "2026-09-13T06:50:00Z"))!!,
            received,
        )
        val next = TrackingFrames.apply(first, TrackingFrames.parse(orderId, frame("2026-09-13T06:39:05Z", lat = 13.1))!!, received)

        assertThat(next.rider?.latitude).isEqualTo(13.1)
        assertThat(next.etaAt).isEqualTo(Instant.parse("2026-09-13T06:50:00Z"))
    }

    @Test
    fun `frames for another order, other event types and malformed frames are not rider positions`() {
        assertThat(TrackingFrames.parse(orderId, frame("2026-09-13T06:39:00Z", order = "someone-else"))).isNull()
        assertThat(TrackingFrames.parse(orderId, frame("2026-09-13T06:39:00Z", type = "food.order.status_changed"))).isNull()
        assertThat(TrackingFrames.parse(orderId, frame("not a time"))).isNull()
        assertThat(TrackingFrames.isOrderChange(orderId, frame("2026-09-13T06:39:00Z", type = "food.order.status_changed"))).isTrue()
        assertThat(TrackingFrames.isOrderChange(orderId, frame("2026-09-13T06:39:00Z"))).isFalse()
    }

    // ── The delivery code ────────────────────────────────────────────────

    @Test
    fun `the delivery code is shown only while the rider has the food`() {
        val golden = fixture("order_get_200_out_for_delivery.json", FeastOrderDto.serializer())
        assertThat(DeliveryCodeRule.visibleCode(golden)).isEqualTo("7390")
        assertThat(DeliveryCodeRule.visibleCode(golden.copy(status = "PICKED_UP"))).isEqualTo("7390")

        val before = listOf("PLACED", "PAYMENT_PENDING", "CONFIRMED", "PREPARING", "READY_FOR_PICKUP", "DELIVERY_ASSIGNED")
        val after = listOf("DELIVERED", "CANCELLED_BY_CUSTOMER", "CANCELLED_BY_RESTAURANT")
        (before + after).forEach { status ->
            // Even if a server bug leaked the code, the app does not show it.
            assertThat(DeliveryCodeRule.visibleCode(golden.copy(status = status))).isNull()
        }
        assertThat(DeliveryCodeRule.visibleCode(fixture("order_get_200.json", FeastOrderDto.serializer()))).isNull()
    }

    // ── Address: rationale before the permission prompt ─────────────────

    private class DeniedLocation : CurrentLocationSource {
        override fun hasPermission() = false
        override suspend fun current(): Coordinates? = null
    }

    private object NoGeocoder : AddressLookup {
        override suspend fun reverse(coordinates: Coordinates): LookedUpAddress? = null
        override suspend fun forward(query: String): Coordinates? = null
    }

    @Test
    fun `the rationale is shown before the system permission prompt is requested`() = runTest(dispatcher) {
        val model = AddAddressViewModel(FakeFeastApi().repository(), FeastSession(), DeniedLocation(), NoGeocoder)
        val effects = mutableListOf<LocationEffect>()
        backgroundScope.launch(dispatcher) { model.effects.collect { effects += it } }

        // A permission result before any rationale is ignored.
        model.onPermissionResult(granted = true, canAskAgain = true)
        model.onUseCurrentLocation()
        advanceUntilIdle()
        runCurrent()

        assertThat(model.state.value.step).isEqualTo(LocationStep.ExplainingPermission)
        assertThat(effects).isEmpty()

        model.onRationaleAccepted()
        advanceUntilIdle()
        runCurrent()

        assertThat(effects).containsExactly(LocationEffect.RequestPermission)
        assertThat(model.state.value.step).isEqualTo(LocationStep.AwaitingPermission)
    }

    @Test
    fun `dismissing the rationale never requests the permission`() = runTest(dispatcher) {
        val model = AddAddressViewModel(FakeFeastApi().repository(), FeastSession(), DeniedLocation(), NoGeocoder)
        val effects = mutableListOf<LocationEffect>()
        backgroundScope.launch(dispatcher) { model.effects.collect { effects += it } }

        model.onUseCurrentLocation()
        model.onRationaleDismissed()
        model.onRationaleAccepted()
        advanceUntilIdle()
        runCurrent()

        assertThat(effects).isEmpty()
        assertThat(model.state.value.step).isEqualTo(LocationStep.Idle)
    }
}
