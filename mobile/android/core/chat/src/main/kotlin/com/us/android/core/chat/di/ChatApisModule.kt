package com.us.android.core.chat.di

import com.us.android.core.chat.data.CommunityApi
import com.us.android.core.chat.data.CommunityMembershipApi
import com.us.android.core.chat.data.CommunityUpdatesApi
import com.us.android.core.chat.data.GroupInviteApi
import com.us.android.core.chat.data.PeopleLookupApi
import com.us.android.core.chat.data.SuggestionsApi
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import retrofit2.Retrofit
import javax.inject.Singleton

/**
 * The 2026-09-05 chat surfaces — invite links, communities, suggestions
 * and the people picker — on the same authenticated Retrofit as [ChatModule]
 * builds the chat endpoints from. No client, no base URL, no converter of
 * their own, for the same reason: one token refresher.
 */
@Module
@InstallIn(SingletonComponent::class)
object ChatApisModule {

    @Provides
    @Singleton
    fun provideGroupInviteApi(retrofit: Retrofit): GroupInviteApi = retrofit.create(GroupInviteApi::class.java)

    @Provides
    @Singleton
    fun provideCommunityApi(retrofit: Retrofit): CommunityApi = retrofit.create(CommunityApi::class.java)

    @Provides
    @Singleton
    fun provideCommunityMembershipApi(retrofit: Retrofit): CommunityMembershipApi =
        retrofit.create(CommunityMembershipApi::class.java)

    @Provides
    @Singleton
    fun provideCommunityUpdatesApi(retrofit: Retrofit): CommunityUpdatesApi =
        retrofit.create(CommunityUpdatesApi::class.java)

    @Provides
    @Singleton
    fun provideSuggestionsApi(retrofit: Retrofit): SuggestionsApi = retrofit.create(SuggestionsApi::class.java)

    @Provides
    @Singleton
    fun providePeopleLookupApi(retrofit: Retrofit): PeopleLookupApi = retrofit.create(PeopleLookupApi::class.java)
}
