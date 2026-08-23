package com.us.android.core.profile.di

import com.us.android.core.profile.data.AccountSecurityApi
import com.us.android.core.profile.data.DeviceSecurityApi
import com.us.android.core.profile.data.NotificationSettingsApi
import com.us.android.core.profile.data.PrivacySettingsApi
import com.us.android.core.profile.data.ProfileApi
import com.us.android.core.profile.data.ProfileDetailsApi
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

    @Provides
    @Singleton
    fun providePrivacySettingsApi(retrofit: Retrofit): PrivacySettingsApi =
        retrofit.create(PrivacySettingsApi::class.java)

    @Provides
    @Singleton
    fun provideNotificationSettingsApi(retrofit: Retrofit): NotificationSettingsApi =
        retrofit.create(NotificationSettingsApi::class.java)

    @Provides
    @Singleton
    fun provideAccountSecurityApi(retrofit: Retrofit): AccountSecurityApi =
        retrofit.create(AccountSecurityApi::class.java)

    @Provides
    @Singleton
    fun provideDeviceSecurityApi(retrofit: Retrofit): DeviceSecurityApi =
        retrofit.create(DeviceSecurityApi::class.java)

    @Provides
    @Singleton
    fun provideProfileDetailsApi(retrofit: Retrofit): ProfileDetailsApi =
        retrofit.create(ProfileDetailsApi::class.java)
}
