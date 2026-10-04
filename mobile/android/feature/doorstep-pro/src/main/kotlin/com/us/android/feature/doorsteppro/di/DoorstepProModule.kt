package com.us.android.feature.doorsteppro.di

import com.us.android.core.realtime.RealtimeEvent
import com.us.android.core.realtime.RealtimeTokenSource
import com.us.android.core.realtime.SseClient
import com.us.android.feature.doorsteppro.data.DoorstepProApi
import com.us.android.feature.doorsteppro.data.DoorstepProRepository
import com.us.android.feature.doorsteppro.data.RealDoorstepProRepository
import com.us.android.feature.doorsteppro.deeplink.DigiLockerStateStore
import com.us.android.feature.doorsteppro.domain.ProClock
import com.us.android.feature.doorsteppro.location.AndroidPlaceLookup
import com.us.android.feature.doorsteppro.location.CurrentLocationSource
import com.us.android.feature.doorsteppro.location.FusedCurrentLocationSource
import com.us.android.feature.doorsteppro.location.PlaceLookup
import com.us.android.feature.doorsteppro.realtime.ProTopics
import com.us.android.feature.doorsteppro.store.LocationDisclosureStore
import com.us.android.feature.doorsteppro.store.OnboardingMemory
import com.us.android.feature.doorsteppro.store.SharedPrefsDigiLockerStateStore
import com.us.android.feature.doorsteppro.store.SharedPrefsLocationDisclosureStore
import com.us.android.feature.doorsteppro.store.SharedPrefsOnboardingMemory
import com.us.android.feature.doorsteppro.store.SharedPrefsVisitMemory
import com.us.android.feature.doorsteppro.store.VisitMemory
import com.us.android.feature.doorsteppro.upload.PhotoUploads
import com.us.android.feature.doorsteppro.upload.ProPhotoUploader
import dagger.Binds
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import kotlinx.coroutines.flow.Flow
import retrofit2.Retrofit
import javax.inject.Singleton

/** The professional's live topic as a port, so Home's ViewModel tests on the JVM. */
fun interface ProEventStream {
    fun events(userId: String, tokenSource: RealtimeTokenSource): Flow<RealtimeEvent>
}

/**
 * Doorstep Pro's endpoints on the platform's shared Retrofit client (token
 * authenticator, retry, tracing) — never a bespoke client — plus the device
 * ports its screens use, bound so the ViewModels test on the JVM.
 */
@Module
@InstallIn(SingletonComponent::class)
abstract class DoorstepProModule {

    @Binds
    abstract fun bindRepository(impl: RealDoorstepProRepository): DoorstepProRepository

    @Binds
    abstract fun bindCurrentLocationSource(impl: FusedCurrentLocationSource): CurrentLocationSource

    @Binds
    abstract fun bindPlaceLookup(impl: AndroidPlaceLookup): PlaceLookup

    @Binds
    abstract fun bindPhotoUploads(impl: ProPhotoUploader): PhotoUploads

    @Binds
    abstract fun bindDigiLockerStateStore(impl: SharedPrefsDigiLockerStateStore): DigiLockerStateStore

    @Binds
    abstract fun bindLocationDisclosureStore(impl: SharedPrefsLocationDisclosureStore): LocationDisclosureStore

    @Binds
    abstract fun bindOnboardingMemory(impl: SharedPrefsOnboardingMemory): OnboardingMemory

    @Binds
    abstract fun bindVisitMemory(impl: SharedPrefsVisitMemory): VisitMemory

    companion object {
        @Provides
        @Singleton
        fun provideDoorstepProApi(retrofit: Retrofit): DoorstepProApi = retrofit.create(DoorstepProApi::class.java)

        @Provides
        fun provideProClock(): ProClock = ProClock.System

        /** doorstep.pro.<user_id> through notification-service SSE; the caller supplies the token source (ProRealtimeTokenSource). */
        @Provides
        fun provideProEventStream(sseClient: SseClient): ProEventStream =
            ProEventStream { userId, tokenSource -> sseClient.connect(ProTopics.forPro(userId), tokenSource) }
    }
}
