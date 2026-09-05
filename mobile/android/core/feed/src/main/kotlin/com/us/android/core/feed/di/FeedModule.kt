package com.us.android.core.feed.di

import com.us.android.core.feed.data.ChannelApi
import com.us.android.core.feed.data.FeedApi
import com.us.android.core.feed.data.FeedItemHydrator
import com.us.android.core.feed.data.HashtagPostHydrator
import com.us.android.core.feed.data.VideoFeedApi
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

    @Provides
    @Singleton
    fun provideVideoFeedApi(retrofit: Retrofit): VideoFeedApi = retrofit.create(VideoFeedApi::class.java)

    @Provides
    @Singleton
    fun provideChannelApi(retrofit: Retrofit): ChannelApi = retrofit.create(ChannelApi::class.java)

    /** The real hydrator behind the seam; tests bind the identity instead. */
    @Provides
    fun provideFeedItemHydrator(impl: HashtagPostHydrator): FeedItemHydrator = impl
}
