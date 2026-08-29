package com.us.android.core.location.di

import com.us.android.core.location.LocationTracker
import com.us.android.core.location.MopeduLocationTracker
import dagger.Binds
import dagger.Module
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import javax.inject.Singleton

@Module
@InstallIn(SingletonComponent::class)
abstract class LocationModule {
    @Binds
    @Singleton
    abstract fun bindLocationTracker(impl: MopeduLocationTracker): LocationTracker
}
