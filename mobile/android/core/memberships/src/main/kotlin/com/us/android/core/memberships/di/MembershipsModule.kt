package com.us.android.core.memberships.di

import com.us.android.core.memberships.data.MembershipsApi
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import retrofit2.Retrofit
import javax.inject.Singleton

/** Creates the membership endpoints from the app-wide [Retrofit]. No second stack. */
@Module
@InstallIn(SingletonComponent::class)
object MembershipsModule {

    @Provides
    @Singleton
    fun provideMembershipsApi(retrofit: Retrofit): MembershipsApi = retrofit.create(MembershipsApi::class.java)
}
