package com.us.android.feature.feed.di

import com.us.android.feature.feed.data.FeedApi
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import retrofit2.Retrofit
import javax.inject.Singleton

/** Creates the feed endpoints from the app-wide [Retrofit]. No second stack. */
@Module
@InstallIn(SingletonComponent::class)
object FeedModule {

    @Provides
    @Singleton
    fun provideFeedApi(retrofit: Retrofit): FeedApi = retrofit.create(FeedApi::class.java)
}
