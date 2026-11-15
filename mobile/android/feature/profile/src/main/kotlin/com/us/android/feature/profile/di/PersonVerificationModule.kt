package com.us.android.feature.profile.di

import com.us.android.feature.profile.data.MediaPersonSelfieUploader
import com.us.android.feature.profile.data.PersonSelfieUploader
import com.us.android.feature.profile.data.PersonVerificationApi
import com.us.android.feature.profile.data.PersonVerificationRepository
import com.us.android.feature.profile.data.PersonVerificationSource
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import retrofit2.Retrofit
import javax.inject.Singleton

/**
 * The person-verification endpoints (2026-11-15), created from the app-wide [Retrofit] that
 * `:core:network` builds — never a client of this feature's own, which would fork token refresh.
 */
@Module
@InstallIn(SingletonComponent::class)
object PersonVerificationModule {

    @Provides
    @Singleton
    fun providePersonVerificationApi(retrofit: Retrofit): PersonVerificationApi =
        retrofit.create(PersonVerificationApi::class.java)

    @Provides
    fun providePersonVerificationSource(impl: PersonVerificationRepository): PersonVerificationSource = impl

    @Provides
    fun providePersonSelfieUploader(impl: MediaPersonSelfieUploader): PersonSelfieUploader = impl
}
