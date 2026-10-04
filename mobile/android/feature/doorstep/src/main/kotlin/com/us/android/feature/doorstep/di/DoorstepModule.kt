package com.us.android.feature.doorstep.di

import com.us.android.core.realtime.RealtimeEvent
import com.us.android.core.realtime.SseClient
import com.us.android.feature.doorstep.address.AddressLookup
import com.us.android.feature.doorstep.address.AndroidAddressLookup
import com.us.android.feature.doorstep.address.CurrentLocationSource
import com.us.android.feature.doorstep.address.FusedCurrentLocationSource
import com.us.android.feature.doorstep.data.DoorstepApi
import com.us.android.feature.doorstep.data.DoorstepRepository
import com.us.android.feature.doorstep.data.RealDoorstepRepository
import com.us.android.feature.doorstep.realtime.BookingEventStream
import com.us.android.feature.doorstep.realtime.DoorstepClock
import com.us.android.feature.doorstep.realtime.DoorstepRealtimeTokenSource
import com.us.android.feature.doorstep.realtime.DoorstepTopics
import dagger.Binds
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import kotlinx.coroutines.flow.Flow
import retrofit2.Retrofit
import javax.inject.Singleton

/**
 * Doorstep's endpoints on the platform's shared Retrofit client (token
 * authenticator, retry, tracing) — never a bespoke client — plus the device
 * ports its screens use, bound so the ViewModels test on the JVM.
 *
 * The location bindings are qualified by type only inside this feature's
 * package: Feast binds its OWN `CurrentLocationSource` (a different type in
 * another package), so the two never collide in the one Momentum graph.
 */
@Module
@InstallIn(SingletonComponent::class)
abstract class DoorstepModule {

    @Binds
    abstract fun bindRepository(impl: RealDoorstepRepository): DoorstepRepository

    @Binds
    abstract fun bindCurrentLocationSource(impl: FusedCurrentLocationSource): CurrentLocationSource

    @Binds
    abstract fun bindAddressLookup(impl: AndroidAddressLookup): AddressLookup

    companion object {
        @Provides
        @Singleton
        fun provideDoorstepApi(retrofit: Retrofit): DoorstepApi = retrofit.create(DoorstepApi::class.java)

        @Provides
        fun provideDoorstepClock(): DoorstepClock = DoorstepClock.System

        /**
         * The booking's live topic through notification-service SSE, with a
         * fresh booking-scoped token for every (re)connect.
         */
        @Provides
        fun provideBookingEventStream(sseClient: SseClient, repository: DoorstepRepository): BookingEventStream =
            object : BookingEventStream {
                override fun events(bookingId: String): Flow<RealtimeEvent> =
                    sseClient.connect(DoorstepTopics.forBooking(bookingId), DoorstepRealtimeTokenSource(repository, bookingId))
            }
    }
}
