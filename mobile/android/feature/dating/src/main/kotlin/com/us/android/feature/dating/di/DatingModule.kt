package com.us.android.feature.dating.di

import com.us.android.feature.dating.location.CurrentLocationSource
import com.us.android.feature.dating.location.FusedCurrentLocationSource
import com.us.android.feature.dating.network.DatingApi
import com.us.android.feature.dating.photos.PhotoUploader
import com.us.android.feature.dating.photos.MediaPhotoUploader
import com.us.android.feature.dating.selfie.MediaSelfieVideoUploader
import com.us.android.feature.dating.selfie.SelfieVideoUploader
import dagger.Binds
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import retrofit2.Retrofit
import javax.inject.Singleton

/**
 * Dating's endpoints on the platform's shared Retrofit client (token
 * authenticator, retry, tracing, CSRF) — never a bespoke client — plus the
 * device ports its screens use, bound so the ViewModels test on the JVM.
 */
@Module
@InstallIn(SingletonComponent::class)
abstract class DatingModule {

    @Binds
    abstract fun bindCurrentLocationSource(impl: FusedCurrentLocationSource): CurrentLocationSource

    @Binds
    abstract fun bindPhotoUploader(impl: MediaPhotoUploader): PhotoUploader

    @Binds
    abstract fun bindSelfieVideoUploader(impl: MediaSelfieVideoUploader): SelfieVideoUploader

    companion object {
        @Provides
        @Singleton
        fun provideDatingApi(retrofit: Retrofit): DatingApi = retrofit.create(DatingApi::class.java)
    }
}
