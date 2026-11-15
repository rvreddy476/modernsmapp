package com.us.android.feature.profile.di

import com.us.android.feature.profile.data.OfficialMarkApi
import com.us.android.feature.profile.data.OfficialMarkRepository
import com.us.android.feature.profile.data.OfficialMarkSource
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import retrofit2.Retrofit
import javax.inject.Singleton

/**
 * The Official mark endpoints (2026-11-15), created from the app-wide [Retrofit] that
 * `:core:network` builds — never a client of this feature's own, which would fork token refresh.
 */
@Module
@InstallIn(SingletonComponent::class)
object OfficialMarkModule {

    @Provides
    @Singleton
    fun provideOfficialMarkApi(retrofit: Retrofit): OfficialMarkApi = retrofit.create(OfficialMarkApi::class.java)

    @Provides
    fun provideOfficialMarkSource(impl: OfficialMarkRepository): OfficialMarkSource = impl
}
