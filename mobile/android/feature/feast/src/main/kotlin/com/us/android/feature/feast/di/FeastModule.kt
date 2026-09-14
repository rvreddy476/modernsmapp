package com.us.android.feature.feast.di

import com.us.android.core.food.realtime.FoodRealtimeScope
import com.us.android.core.food.realtime.FoodRealtimeTokens
import com.us.android.core.realtime.RealtimeEvent
import com.us.android.core.realtime.SseClient
import com.us.android.feature.feast.address.AddressLookup
import com.us.android.feature.feast.address.AndroidAddressLookup
import com.us.android.feature.feast.address.CurrentLocationSource
import com.us.android.feature.feast.address.FusedCurrentLocationSource
import com.us.android.feature.feast.tracking.FeastClock
import com.us.android.feature.feast.tracking.OrderEventStream
import com.us.android.feature.feast.tracking.OrderTopics
import dagger.Binds
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import kotlinx.coroutines.flow.Flow

@Module
@InstallIn(SingletonComponent::class)
abstract class FeastModule {

    @Binds
    abstract fun bindCurrentLocationSource(impl: FusedCurrentLocationSource): CurrentLocationSource

    @Binds
    abstract fun bindAddressLookup(impl: AndroidAddressLookup): AddressLookup

    companion object {
        @Provides
        fun provideFeastClock(): FeastClock = FeastClock.System

        /**
         * The order's live topic through notification-service SSE, with a fresh
         * `{scope:"order", id}` token for every (re)connect.
         */
        @Provides
        fun provideOrderEventStream(sseClient: SseClient, tokens: FoodRealtimeTokens): OrderEventStream =
            object : OrderEventStream {
                override fun events(orderId: String): Flow<RealtimeEvent> =
                    sseClient.connect(OrderTopics.forOrder(orderId), tokens.forScope(FoodRealtimeScope.Order(orderId)))
            }
    }
}
