package com.us.android.feature.feed.di

import com.us.android.feature.feed.data.FeedApi
import com.us.android.feature.feed.data.FeedItemHydrator
import com.us.android.feature.feed.data.HashtagPostHydrator
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

    /** The real hydrator behind the seam; tests bind the identity instead. */
    @Provides
    fun provideFeedItemHydrator(impl: HashtagPostHydrator): FeedItemHydrator = impl
}
