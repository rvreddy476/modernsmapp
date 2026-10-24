package com.us.android.feature.tube.di

import com.us.android.core.common.result.AppResult
import com.us.android.core.engagement.data.CommentActionsRepository
import com.us.android.core.feed.data.credits.CreditRequest
import com.us.android.core.feed.data.credits.CreditsRepository
import com.us.android.core.network.Paged
import com.us.android.feature.tube.data.CreditRequestsSource
import com.us.android.feature.tube.data.VideoSeriesApi
import com.us.android.feature.tube.data.WatchElementsApi
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

    /** End screens and cards as a viewer sees them (2026-10-24). */
    @Provides
    @Singleton
    fun provideWatchElementsApi(retrofit: Retrofit): WatchElementsApi = retrofit.create(WatchElementsApi::class.java)

    /** The Creator Hub's reads (T-31). */
    @Provides
    @Singleton
    fun provideCreatorHubApi(retrofit: Retrofit): CreatorHubApi = retrofit.create(CreatorHubApi::class.java)

    /** The credit requests screen (2026-10-23) reads and answers through the credits repository. */
    @Provides
    fun provideCreditRequestsSource(credits: CreditsRepository): CreditRequestsSource =
        object : CreditRequestsSource {
            override suspend fun requests(cursor: String?): AppResult<Paged<CreditRequest>> = credits.requests(cursor)

            override suspend fun accept(creditId: String): AppResult<Unit> = credits.accept(creditId)

            override suspend fun decline(creditId: String): AppResult<Unit> = credits.decline(creditId)

            override suspend fun removeMe(creditId: String): AppResult<Unit> = credits.removeMe(creditId)
        }

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
