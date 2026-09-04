package com.us.android.feature.tube.di

import com.us.android.feature.tube.data.WatchProgressApi
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import retrofit2.Retrofit
import javax.inject.Singleton

/** Creates Tube's endpoints from the app-wide [Retrofit]. No second stack. */
@Module
@InstallIn(SingletonComponent::class)
object TubeModule {

    @Provides
    @Singleton
    fun provideWatchProgressApi(retrofit: Retrofit): WatchProgressApi = retrofit.create(WatchProgressApi::class.java)
}
