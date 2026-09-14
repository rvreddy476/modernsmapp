package com.us.android.feature.rider.di

import com.us.android.feature.rider.digilocker.DigiLockerStateStore
import com.us.android.feature.rider.digilocker.SharedPrefsDigiLockerStateStore
import com.us.android.feature.rider.home.LocationDisclosureStore
import com.us.android.feature.rider.home.SharedPrefsLocationDisclosureStore
import com.us.android.feature.rider.offers.RiderClock
import dagger.Binds
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent

@Module
@InstallIn(SingletonComponent::class)
abstract class RiderModule {

    // Job navigation needs no binding: JobLocations maps the assignment's own
    // restaurant, drop and navigation links (food-service 610a2acd), purely.

    @Binds
    abstract fun bindDigiLockerStateStore(impl: SharedPrefsDigiLockerStateStore): DigiLockerStateStore

    @Binds
    abstract fun bindLocationDisclosureStore(impl: SharedPrefsLocationDisclosureStore): LocationDisclosureStore

    companion object {
        @Provides
        fun provideRiderClock(): RiderClock = RiderClock.System
    }
}
