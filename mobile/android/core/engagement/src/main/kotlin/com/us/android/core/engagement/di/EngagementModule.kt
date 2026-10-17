package com.us.android.core.engagement.di

import com.us.android.core.engagement.data.CommentActionsApi
import com.us.android.core.engagement.data.EngagementApi
import com.us.android.core.engagement.data.EngagementRepository
import com.us.android.core.engagement.data.EngagementStore
import com.us.android.core.engagement.data.EngagementWrites
import com.us.android.core.engagement.data.PostLifecycleApi
import com.us.android.core.engagement.data.ReportApi
import com.us.android.core.engagement.data.RepostReads
import com.us.android.core.engagement.data.RepostStateApi
import com.us.android.core.engagement.data.RepostStateRepository
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import retrofit2.Retrofit
import javax.inject.Singleton

/**
 * Creates the engagement endpoints from the app-wide [Retrofit].
 *
 * No client, no base URL, no converter. A module that assembles its own client
 * forks token refresh, and two refreshers racing a rotating refresh token sign
 * the user out.
 */
@Module
@InstallIn(SingletonComponent::class)
object EngagementModule {

    @Provides
    @Singleton
    fun provideEngagementApi(retrofit: Retrofit): EngagementApi =
        retrofit.create(EngagementApi::class.java)

    /** Comment-level routes: replies, likes, delete, pin and heart (audit T-17). */
    @Provides
    @Singleton
    fun provideCommentActionsApi(retrofit: Retrofit): CommentActionsApi =
        retrofit.create(CommentActionsApi::class.java)

    /** trust-safety's report intake, on the same authenticated client. */
    @Provides
    @Singleton
    fun provideReportApi(retrofit: Retrofit): ReportApi = retrofit.create(ReportApi::class.java)

    /** Soft delete / restore / recently deleted, on the same authenticated client. */
    @Provides
    @Singleton
    fun providePostLifecycleApi(retrofit: Retrofit): PostLifecycleApi =
        retrofit.create(PostLifecycleApi::class.java)

    /**
     * Binds the writes interface to the real repository.
     *
     * [EngagementStore] depends on the interface rather than the concrete type
     * so its ordering behaviour can be tested against responses that resolve
     * out of order; the graph still has exactly one implementation.
     */
    @Provides
    @Singleton
    fun provideEngagementWrites(repository: EngagementRepository): EngagementWrites = repository

    /** The viewer's repost of one post, for the share sheet's Repost row (2026-10-17). */
    @Provides
    @Singleton
    fun provideRepostStateApi(retrofit: Retrofit): RepostStateApi = retrofit.create(RepostStateApi::class.java)

    @Provides
    fun provideRepostReads(repository: RepostStateRepository): RepostReads = repository
}
