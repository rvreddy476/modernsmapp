package com.us.android.core.chat.hidden

import com.us.android.core.common.di.ApplicationScope
import com.us.android.core.common.session.SessionTeardownTask
import com.us.android.core.datastore.SettingsDataStore
import dagger.Binds
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import dagger.multibindings.IntoSet
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import retrofit2.Retrofit
import javax.inject.Inject
import javax.inject.Singleton

/**
 * This install's device id for `X-Device-Id`: the same stable per-install id
 * offline copies already keep in DataStore (made on first use). Read once
 * into memory off the main thread; the interceptor that asks runs on
 * OkHttp's threads, so the one blocking read it may do never touches the UI.
 */
@Singleton
class DataStoreHiddenDeviceId @Inject constructor(
    private val settings: SettingsDataStore,
    @ApplicationScope scope: CoroutineScope,
) : HiddenDeviceId {

    @Volatile private var cached: String? = null

    init {
        scope.launch { cached = runCatching { settings.offlineDeviceId() }.getOrNull() }
    }

    override fun get(): String? =
        cached ?: runCatching { runBlocking { settings.offlineDeviceId() } }.getOrNull()?.also { cached = it }
}

/** Sign-out: every hidden-chat token, id, list, setting and sealed password leaves this device. */
@Singleton
class HiddenChatsTeardown @Inject constructor(
    private val hidden: dagger.Lazy<HiddenChats>,
) : SessionTeardownTask {
    override suspend fun onSignOut() {
        hidden.get().forgetForSignOut()
    }
}

@Module
@InstallIn(SingletonComponent::class)
abstract class HiddenChatsBindings {

    @Binds
    abstract fun bindTokenStore(impl: HiddenChatTokens): HiddenTokenStore

    @Binds
    abstract fun bindDeviceId(impl: DataStoreHiddenDeviceId): HiddenDeviceId

    @Binds
    abstract fun bindUnlockVault(impl: KeystoreHiddenUnlockVault): HiddenUnlockVault

    /** Seals hidden chats' queued sends; its own background Keystore key. */
    @Binds
    abstract fun bindOutboxCipher(impl: KeystoreHiddenOutboxCipher): HiddenOutboxCipher

    @Binds
    @IntoSet
    abstract fun bindTeardown(impl: HiddenChatsTeardown): SessionTeardownTask

    companion object {
        /** On the CHAT Retrofit: its client adds the device id and never replays a refused secret. */
        @Provides
        @Singleton
        fun provideHiddenChatsApi(@com.us.android.core.chat.di.ChatRetrofit retrofit: Retrofit): HiddenChatsApi =
            retrofit.create(HiddenChatsApi::class.java)
    }
}
