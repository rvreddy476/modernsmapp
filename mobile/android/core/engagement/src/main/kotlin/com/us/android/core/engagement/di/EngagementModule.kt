package com.us.android.core.engagement.di

import com.us.android.core.engagement.data.EngagementApi
import com.us.android.core.engagement.data.EngagementRepository
import com.us.android.core.engagement.data.EngagementStore
import com.us.android.core.engagement.data.EngagementWrites
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
}
