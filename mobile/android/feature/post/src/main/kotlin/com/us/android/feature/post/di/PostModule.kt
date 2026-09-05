package com.us.android.feature.post.di

import com.us.android.feature.post.data.HashtagSearchApi
import com.us.android.feature.post.data.PeopleSearchApi
import com.us.android.feature.post.data.PostApi
import com.us.android.feature.post.data.PostCategoriesApi
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import retrofit2.Retrofit
import javax.inject.Singleton

/**
 * Creates the post endpoints from the app-wide [Retrofit].
 *
 * No client, no base URL, no interceptor. A feature that assembles its own
 * stack forks token refresh, and two refreshers racing a rotating refresh
 * token sign the user out.
 */
@Module
@InstallIn(SingletonComponent::class)
object PostModule {

    @Provides
    @Singleton
    fun providePostApi(retrofit: Retrofit): PostApi = retrofit.create(PostApi::class.java)

    /** The reel form's category picker. */
    @Provides
    @Singleton
    fun providePostCategoriesApi(retrofit: Retrofit): PostCategoriesApi =
        retrofit.create(PostCategoriesApi::class.java)

    /** The reel form's "Tag people" search — search-service through the gateway. */
    @Provides
    @Singleton
    fun providePeopleSearchApi(retrofit: Retrofit): PeopleSearchApi =
        retrofit.create(PeopleSearchApi::class.java)

    /** The details step's hashtag suggestions — post-service through the gateway. */
    @Provides
    @Singleton
    fun provideHashtagSearchApi(retrofit: Retrofit): HashtagSearchApi =
        retrofit.create(HashtagSearchApi::class.java)
}
