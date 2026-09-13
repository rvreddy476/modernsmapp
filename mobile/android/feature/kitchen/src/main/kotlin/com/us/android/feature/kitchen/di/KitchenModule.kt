package com.us.android.feature.kitchen.di

import com.us.android.feature.kitchen.location.AddressLookup
import com.us.android.feature.kitchen.location.AndroidAddressLookup
import com.us.android.feature.kitchen.location.CurrentLocationSource
import com.us.android.feature.kitchen.location.FusedCurrentLocationSource
import com.us.android.feature.kitchen.menu.MenuExtrasRepository
import com.us.android.feature.kitchen.menu.UnavailableMenuExtrasRepository
import com.us.android.feature.kitchen.queue.KitchenClock
import com.us.android.feature.kitchen.queue.PendingB5RestaurantRealtimeTokens
import com.us.android.feature.kitchen.queue.RestaurantRealtimeTokens
import dagger.Binds
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent

@Module
@InstallIn(SingletonComponent::class)
abstract class KitchenModule {

    /**
     * WIRE AFTER B5: swap this binding for the scoped-token implementation.
     * See [PendingB5RestaurantRealtimeTokens]. Until then the queue polls.
     */
    @Binds
    abstract fun bindRestaurantRealtimeTokens(impl: PendingB5RestaurantRealtimeTokens): RestaurantRealtimeTokens

    @Binds
    abstract fun bindCurrentLocationSource(impl: FusedCurrentLocationSource): CurrentLocationSource

    @Binds
    abstract fun bindAddressLookup(impl: AndroidAddressLookup): AddressLookup

    /** ROUTE GAP: food-service has no partner variant or add-on routes. */
    @Binds
    abstract fun bindMenuExtrasRepository(impl: UnavailableMenuExtrasRepository): MenuExtrasRepository

    companion object {
        @Provides
        fun provideKitchenClock(): KitchenClock = KitchenClock.System
    }
}
