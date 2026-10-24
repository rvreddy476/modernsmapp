package com.us.android.core.profile.di

import com.us.android.core.profile.data.AccountDataApi
import com.us.android.core.profile.data.AccountDataRepository
import com.us.android.core.profile.data.AccountDataSource
import com.us.android.core.profile.data.AccountSecurityApi
import com.us.android.core.profile.data.AuthorPostCountsApi
import com.us.android.core.profile.data.BlockedAccountsApi
import com.us.android.core.profile.data.BlockedAccountsRepository
import com.us.android.core.profile.data.BlockedAccountsSource
import com.us.android.core.profile.data.DataStoreKeywordFiltersCache
import com.us.android.core.profile.data.DataStoreModulePreferencesCache
import com.us.android.core.profile.data.DataStoreWellbeingGuardCache
import com.us.android.core.profile.data.DeviceSecurityApi
import com.us.android.core.profile.data.FeaturesApi
import com.us.android.core.profile.data.FollowListApi
import com.us.android.core.profile.data.GraphCountsApi
import com.us.android.core.profile.data.KeywordFiltersApi
import com.us.android.core.profile.data.KeywordFiltersCache
import com.us.android.core.profile.data.ManageAccountApi
import com.us.android.core.profile.data.ModulePreferencesApi
import com.us.android.core.profile.data.ModulePreferencesCache
import com.us.android.core.profile.data.MuteApi
import com.us.android.core.profile.data.MuteRepository
import com.us.android.core.profile.data.MuteSource
import com.us.android.core.profile.data.NotificationSettingsApi
import com.us.android.core.profile.data.PrivacySettingsApi
import com.us.android.core.profile.data.ProfileApi
import com.us.android.core.profile.data.ProfileClock
import com.us.android.core.profile.data.ProfileDetailsApi
import com.us.android.core.profile.data.ProfilePanelsApi
import com.us.android.core.profile.data.ProfilePanelsRepository
import com.us.android.core.profile.data.ProfilePanelsSource
import com.us.android.core.profile.data.UsernameLookupApi
import com.us.android.core.profile.data.WellbeingApi
import com.us.android.core.profile.data.WellbeingGuardCache
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

    /** `GET /v1/features` (24 Oct 2026): which parked products the gateway serves. */
    @Provides
    @Singleton
    fun provideFeaturesApi(retrofit: Retrofit): FeaturesApi = retrofit.create(FeaturesApi::class.java)

    @Provides
    @Singleton
    fun provideUsernameLookupApi(retrofit: Retrofit): UsernameLookupApi = retrofit.create(UsernameLookupApi::class.java)

    @Provides
    @Singleton
    fun provideGraphCountsApi(retrofit: Retrofit): GraphCountsApi = retrofit.create(GraphCountsApi::class.java)

    @Provides
    @Singleton
    fun provideFollowListApi(retrofit: Retrofit): FollowListApi = retrofit.create(FollowListApi::class.java)

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

    @Provides
    @Singleton
    fun provideModulePreferencesApi(retrofit: Retrofit): ModulePreferencesApi =
        retrofit.create(ModulePreferencesApi::class.java)

    @Provides
    @Singleton
    fun provideModulePreferencesCache(
        cache: DataStoreModulePreferencesCache,
    ): ModulePreferencesCache = cache

    /** The edit-profile date-of-birth checks read "today" in India from this. */
    @Provides
    fun provideProfileClock(): ProfileClock = ProfileClock.System
}

/**
 * The launch-safety pass's own endpoints: Manage account, Screen time and
 * Content preferences. Split out of [ProfileModule] rather than added to it
 * purely to stay under detekt's per-object function ceiling — there is no
 * ownership distinction, just a line count one.
 */
@Module
@InstallIn(SingletonComponent::class)
object LaunchSafetySettingsModule {

    @Provides
    @Singleton
    fun provideManageAccountApi(retrofit: Retrofit): ManageAccountApi =
        retrofit.create(ManageAccountApi::class.java)

    @Provides
    @Singleton
    fun provideWellbeingApi(retrofit: Retrofit): WellbeingApi = retrofit.create(WellbeingApi::class.java)

    @Provides
    @Singleton
    fun provideKeywordFiltersApi(retrofit: Retrofit): KeywordFiltersApi =
        retrofit.create(KeywordFiltersApi::class.java)

    @Provides
    @Singleton
    fun provideKeywordFiltersCache(cache: DataStoreKeywordFiltersCache): KeywordFiltersCache = cache

    @Provides
    @Singleton
    fun provideWellbeingGuardCache(cache: DataStoreWellbeingGuardCache): WellbeingGuardCache = cache
}

/**
 * post-service's per-author post counts (2026-10-17), the profile header's
 * post number. Its own module for the reason [LaunchSafetySettingsModule]
 * is one: detekt's per-object function ceiling.
 */
@Module
@InstallIn(SingletonComponent::class)
object ProfileCountsModule {

    @Provides
    @Singleton
    fun provideAuthorPostCountsApi(retrofit: Retrofit): AuthorPostCountsApi =
        retrofit.create(AuthorPostCountsApi::class.java)

    /** Settings › Privacy › Blocked accounts (2026-10-22). */
    @Provides
    @Singleton
    fun provideBlockedAccountsApi(retrofit: Retrofit): BlockedAccountsApi =
        retrofit.create(BlockedAccountsApi::class.java)

    /** The page reads through the repository; tests hand it a fake. */
    @Provides
    fun provideBlockedAccountsSource(impl: BlockedAccountsRepository): BlockedAccountsSource = impl
}

/**
 * Mute (2026-10-24): graph-service's mute pair, read through [MuteSource].
 * Connect is gone (founder, 24 Oct 2026), so nothing else of the old
 * connections seam is provided.
 */
@Module
@InstallIn(SingletonComponent::class)
object MuteModule {

    @Provides
    @Singleton
    fun provideMuteApi(retrofit: Retrofit): MuteApi = retrofit.create(MuteApi::class.java)

    @Provides
    fun provideMuteSource(impl: MuteRepository): MuteSource = impl
}

/** The profile's About and Reposts tabs (2026-10-24). */
@Module
@InstallIn(SingletonComponent::class)
object ProfilePanelsModule {

    @Provides
    @Singleton
    fun provideProfilePanelsApi(retrofit: Retrofit): ProfilePanelsApi = retrofit.create(ProfilePanelsApi::class.java)

    @Provides
    fun provideProfilePanelsSource(impl: ProfilePanelsRepository): ProfilePanelsSource = impl
}

/** Settings › Download your data and Request verification (2026-10-24). */
@Module
@InstallIn(SingletonComponent::class)
object AccountDataModule {

    @Provides
    @Singleton
    fun provideAccountDataApi(retrofit: Retrofit): AccountDataApi =
        retrofit.create(AccountDataApi::class.java)

    @Provides
    fun provideAccountDataSource(
        impl: AccountDataRepository,
    ): AccountDataSource = impl
}
