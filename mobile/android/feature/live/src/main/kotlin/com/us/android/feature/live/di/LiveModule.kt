package com.us.android.feature.live.di

import com.us.android.feature.live.data.LiveApi
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import retrofit2.Retrofit
import javax.inject.Singleton

/** Creates the live endpoints from the app-wide [Retrofit]. No second stack. */
@Module
@InstallIn(SingletonComponent::class)
object LiveModule {

    @Provides
    @Singleton
    fun provideLiveApi(retrofit: Retrofit): LiveApi = retrofit.create(LiveApi::class.java)
}
