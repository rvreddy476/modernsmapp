package com.us.android.feature.rider.offers

import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.food.network.DeliveryOfferDto
import com.us.android.core.food.network.DeliveryOffersDto
import com.us.android.core.food.realtime.FoodRealtimeTokens
import com.us.android.core.food.repository.RiderRepository
import com.us.android.core.model.SessionState
import com.us.android.core.realtime.SseClient
import com.us.android.feature.rider.RiderFixtures
import com.us.android.feature.rider.location.RiderDuty
import io.mockk.every
import io.mockk.mockk
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.flow
import java.time.Instant

/** The golden pending offer (delivery_offers_me_get_200.json): expires 2026-09-13 06:30:20Z, 20 s on its countdown. */
internal fun goldenOffer(): DeliveryOfferDto =
    checkNotNull(
        RiderFixtures.success("delivery_offers_me_get_200.json", DeliveryOffersDto.serializer()).value.offers,
    ).single()

internal val GOLDEN_EXPIRY: Instant = Instant.parse("2026-09-13T06:30:20Z")

/** A device clock the test moves by hand. */
internal class HandClock(var now: Instant) : RiderClock {
    override fun now(): Instant = now
}

/** A [RiderOfferWatch] over [rider], with a realtime stream that connects and never speaks. */
internal fun offerWatch(rider: RiderRepository, duty: RiderDuty, clock: RiderClock): RiderOfferWatch {
    val sse = mockk<SseClient>()
    every { sse.connect(any(), any()) } returns flow { awaitCancellation() }
    val session = mockk<SessionStateProvider>()
    val signedIn = SessionState.Authenticated(userId = "u-1", sessionId = "s-1")
    every { session.sessionState } returns MutableStateFlow(signedIn)
    return RiderOfferWatch(rider, duty, sse, mockk<FoodRealtimeTokens>(relaxed = true), session, clock)
}
