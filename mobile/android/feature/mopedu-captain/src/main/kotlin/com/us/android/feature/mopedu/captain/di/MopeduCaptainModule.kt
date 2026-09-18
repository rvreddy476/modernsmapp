package com.us.android.feature.mopedu.captain.di

import com.us.android.feature.mopedu.captain.CaptainClock
import com.us.android.feature.mopedu.captain.data.MopeduCaptainApi
import com.us.android.feature.mopedu.captain.data.MopeduCaptainRepository
import com.us.android.feature.mopedu.captain.data.RealMopeduCaptainRepository
import com.us.android.feature.mopedu.captain.home.LocationDisclosureStore
import com.us.android.feature.mopedu.captain.home.SharedPrefsLocationDisclosureStore
import com.us.android.feature.mopedu.captain.upload.CaptainDocumentUploader
import com.us.android.feature.mopedu.captain.upload.MediaCaptainDocumentUploader
import dagger.Binds
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import retrofit2.Retrofit
import javax.inject.Singleton

/** The captain's endpoints on the platform's shared Retrofit client, plus the ports its screens use. */
@Module
@InstallIn(SingletonComponent::class)
abstract class MopeduCaptainModule {

    @Binds
    abstract fun bindRepository(impl: RealMopeduCaptainRepository): MopeduCaptainRepository

    @Binds
    abstract fun bindDisclosureStore(impl: SharedPrefsLocationDisclosureStore): LocationDisclosureStore

    /** The selfie and the DL / RC photos: :core:media's uploader behind a port the tests fake. */
    @Binds
    abstract fun bindDocumentUploader(impl: MediaCaptainDocumentUploader): CaptainDocumentUploader

    companion object {
        @Provides
        @Singleton
        fun provideMopeduCaptainApi(retrofit: Retrofit): MopeduCaptainApi = retrofit.create(MopeduCaptainApi::class.java)

        @Provides
        fun provideCaptainClock(): CaptainClock = CaptainClock { System.currentTimeMillis() }
    }
}
