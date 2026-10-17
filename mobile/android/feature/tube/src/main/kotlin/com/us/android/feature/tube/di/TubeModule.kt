package com.us.android.feature.tube.di

import com.us.android.core.common.result.AppResult
import com.us.android.core.engagement.data.CommentActionsRepository
import com.us.android.feature.tube.data.VideoSeriesApi
import com.us.android.feature.tube.data.WatchProgressApi
import com.us.android.feature.tube.data.hub.CreatorCommentTools
import com.us.android.feature.tube.data.hub.CreatorHubApi
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

    @Provides
    @Singleton
    fun provideVideoSeriesApi(retrofit: Retrofit): VideoSeriesApi = retrofit.create(VideoSeriesApi::class.java)

    /** The Creator Hub's reads (T-31). */
    @Provides
    @Singleton
    fun provideCreatorHubApi(retrofit: Retrofit): CreatorHubApi = retrofit.create(CreatorHubApi::class.java)

    /** The hub's Heart and Pin (2026-10-17) go through the comments sheet's own repository. */
    @Provides
    fun provideCreatorCommentTools(comments: CommentActionsRepository): CreatorCommentTools =
        object : CreatorCommentTools {
            override suspend fun setHearted(commentId: String, hearted: Boolean): AppResult<Unit> =
                comments.setHearted(commentId, hearted)

            override suspend fun setPinned(commentId: String, pinned: Boolean): AppResult<Unit> =
                comments.setPinned(commentId, pinned)
        }
}
