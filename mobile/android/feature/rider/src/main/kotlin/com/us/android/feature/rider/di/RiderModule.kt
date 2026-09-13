package com.us.android.feature.rider.di

import com.us.android.feature.rider.digilocker.DigiLockerStateStore
import com.us.android.feature.rider.digilocker.SharedPrefsDigiLockerStateStore
import com.us.android.feature.rider.home.LocationDisclosureStore
import com.us.android.feature.rider.home.SharedPrefsLocationDisclosureStore
import com.us.android.feature.rider.job.AssignmentOnlyJobLocations
import com.us.android.feature.rider.job.JobLocations
import com.us.android.feature.rider.offers.RiderClock
import dagger.Binds
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent

@Module
@InstallIn(SingletonComponent::class)
abstract class RiderModule {

    /**
     * ROUTE GAP: no food-service route gives the rider the restaurant's or the
     * customer's coordinates (the assignment carries names only). Navigation
     * hands off by restaurant name; the drop has no target. Swap this binding
     * when the assignment gains pickup/drop coordinates.
     */
    @Binds
    abstract fun bindJobLocations(impl: AssignmentOnlyJobLocations): JobLocations

    @Binds
    abstract fun bindDigiLockerStateStore(impl: SharedPrefsDigiLockerStateStore): DigiLockerStateStore

    @Binds
    abstract fun bindLocationDisclosureStore(impl: SharedPrefsLocationDisclosureStore): LocationDisclosureStore

    companion object {
        @Provides
        fun provideRiderClock(): RiderClock = RiderClock.System
    }
}
