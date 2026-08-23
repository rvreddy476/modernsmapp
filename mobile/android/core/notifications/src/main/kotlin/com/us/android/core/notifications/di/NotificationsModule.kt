package com.us.android.core.notifications.di

import android.content.Context
import android.content.SharedPreferences
import com.us.android.core.common.session.SessionTeardownTask
import com.us.android.core.notifications.data.DeviceApi
import com.us.android.core.notifications.data.NotificationsApi
import com.us.android.core.notifications.data.PushTeardown
import com.us.android.core.notifications.data.PushTokenStore
import dagger.Binds
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import dagger.multibindings.IntoSet
import retrofit2.Retrofit
import javax.inject.Singleton

@Module
@InstallIn(SingletonComponent::class)
object NotificationsModule {

    @Provides
    @Singleton
    fun provideDeviceApi(retrofit: Retrofit): DeviceApi = retrofit.create(DeviceApi::class.java)

    @Provides
    @Singleton
    fun provideNotificationsApi(retrofit: Retrofit): NotificationsApi =
        retrofit.create(NotificationsApi::class.java)

    /**
     * Plain SharedPreferences, not the encrypted store.
     *
     * An FCM token is not a credential: it names a device to Google, and
     * possession of it does not authorize anything against this backend. The
     * encrypted store is reserved for the refresh token, where the threat model
     * is real.
     */
    @Provides
    @Singleton
    fun providePushTokenStore(@ApplicationContext context: Context): PushTokenStore =
        SharedPrefsPushTokenStore(
            context.getSharedPreferences("us_push", Context.MODE_PRIVATE),
        )
}

internal class SharedPrefsPushTokenStore(
    private val prefs: SharedPreferences,
) : PushTokenStore {

    override fun token(): String? = prefs.getString(KEY_TOKEN, null)

    override fun setToken(token: String) {
        prefs.edit().putString(KEY_TOKEN, token).apply()
    }

    override fun registeredToken(): String? = prefs.getString(KEY_REGISTERED, null)

    override fun deviceId(): String? = prefs.getString(KEY_DEVICE_ID, null)

    override fun markRegistered(token: String, deviceId: String) {
        prefs.edit()
            .putString(KEY_REGISTERED, token)
            .putString(KEY_DEVICE_ID, deviceId)
            .apply()
    }

    override fun clearRegistration() {
        // The TOKEN survives: it still identifies this device to FCM and will
        // be reused on the next sign-in. Only the server-side association goes.
        prefs.edit().remove(KEY_REGISTERED).remove(KEY_DEVICE_ID).apply()
    }

    private companion object {
        const val KEY_TOKEN = "fcm_token"
        const val KEY_REGISTERED = "registered_token"
        const val KEY_DEVICE_ID = "device_id"
    }
}

/**
 * Contributes push-token cleanup to sign-out — Slice D.
 *
 * `@IntoSet` rather than a direct call from `:core:auth`: that module must not
 * depend on push. See [com.us.android.core.common.session.SessionTeardownTask].
 */
@Module
@InstallIn(SingletonComponent::class)
abstract class NotificationsTeardownModule {

    @Binds
    @IntoSet
    abstract fun bindPushTeardown(impl: PushTeardown): SessionTeardownTask
}
