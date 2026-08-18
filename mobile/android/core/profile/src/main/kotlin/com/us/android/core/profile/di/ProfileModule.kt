package com.us.android.core.profile.di

import com.us.android.core.profile.data.ProfileApi
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import retrofit2.Retrofit
import javax.inject.Singleton

/**
 * Creates the profile endpoints from the app-wide [Retrofit].
 *
 * Note what is NOT here: no OkHttp client, no base URL, no converter, no
 * interceptor. This module asks for the one `Retrofit` that `:core:network`
 * builds and creates an interface from it. A feature that assembles its own
 * client forks token refresh, and two independent refreshers racing a rotating
 * refresh token sign the user out.
 */
@Module
@InstallIn(SingletonComponent::class)
object ProfileModule {

    @Provides
    @Singleton
    fun provideProfileApi(retrofit: Retrofit): ProfileApi = retrofit.create(ProfileApi::class.java)
}
