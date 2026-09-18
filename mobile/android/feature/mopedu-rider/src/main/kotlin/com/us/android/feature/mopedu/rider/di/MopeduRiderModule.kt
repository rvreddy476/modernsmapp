package com.us.android.feature.mopedu.rider.di

import com.us.android.feature.mopedu.rider.RideClock
import com.us.android.feature.mopedu.rider.data.MopeduRiderApi
import com.us.android.feature.mopedu.rider.data.MopeduRiderRepository
import com.us.android.feature.mopedu.rider.data.RealMopeduRiderRepository
import com.us.android.feature.mopedu.rider.location.CurrentLocationSource
import com.us.android.feature.mopedu.rider.location.FusedCurrentLocationSource
import dagger.Binds
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import retrofit2.Retrofit
import javax.inject.Singleton

/**
 * Mopedu's endpoints on the platform's shared Retrofit client (token
 * authenticator, retry, tracing) — never a bespoke client — plus the device
 * ports its screens use, bound so the ViewModels test on the JVM.
 */
@Module
@InstallIn(SingletonComponent::class)
abstract class MopeduRiderModule {

    @Binds
    abstract fun bindRepository(impl: RealMopeduRiderRepository): MopeduRiderRepository

    @Binds
    abstract fun bindCurrentLocationSource(impl: FusedCurrentLocationSource): CurrentLocationSource

    companion object {
        @Provides
        @Singleton
        fun provideMopeduRiderApi(retrofit: Retrofit): MopeduRiderApi = retrofit.create(MopeduRiderApi::class.java)

        @Provides
        fun provideRideClock(): RideClock = RideClock { System.currentTimeMillis() }
    }
}
