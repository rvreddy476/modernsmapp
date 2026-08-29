package com.us.android.core.call.di

import android.content.Context
import com.us.android.core.call.CallAudioController
import com.us.android.core.call.CallNotifier
import com.us.android.core.call.CallSessionManager
import com.us.android.core.call.data.CallApi
import com.us.android.core.call.data.CallRepository
import com.us.android.core.call.engine.CallEngineFactory
import com.us.android.core.call.engine.WebRtcCallEngine
import com.us.android.core.call.platform.AndroidCallAudioController
import com.us.android.core.call.platform.AndroidCallNotifier
import com.us.android.core.chat.data.ChatSessionManager
import com.us.android.core.common.di.ApplicationScope
import com.us.android.core.common.session.SessionTeardownTask
import dagger.Binds
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import dagger.multibindings.IntoSet
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import retrofit2.Retrofit
import javax.inject.Singleton

@Module
@InstallIn(SingletonComponent::class)
object CallModule {

    @Provides
    @Singleton
    fun provideCallApi(retrofit: Retrofit): CallApi = retrofit.create(CallApi::class.java)

    /** A fresh engine per call; the manager owns its lifecycle. */
    @Provides
    @Singleton
    fun provideCallEngineFactory(
        @ApplicationContext context: Context,
    ): CallEngineFactory = CallEngineFactory { WebRtcCallEngine(context) }

    @Provides
    @Singleton
    fun provideCallSessionManager(
        repository: CallRepository,
        session: ChatSessionManager,
        engineFactory: CallEngineFactory,
        notifier: CallNotifier,
        audio: CallAudioController,
        @ApplicationScope scope: CoroutineScope,
    ): CallSessionManager = CallSessionManager(
        repository = repository,
        session = session,
        engineFactory = engineFactory,
        notifier = notifier,
        audio = audio,
        scope = scope,
    )
}

@Module
@InstallIn(SingletonComponent::class)
object CallPushModule {

    /**
     * CALL-LB-4: a data-only ringing push wakes the calling stack. The
     * payload is only a WAKE-UP — refreshIncoming re-verifies the invite
     * with the server before anything rings, exactly like a socket ring.
     */
    @Provides
    @Singleton
    fun provideIncomingCallPushHandler(
        manager: CallSessionManager,
        @ApplicationScope scope: CoroutineScope,
    ): com.us.android.core.notifications.IncomingCallPushHandler =
        com.us.android.core.notifications.IncomingCallPushHandler { _, _ ->
            scope.launch { manager.refreshIncoming() }
        }
}

@Module
@InstallIn(SingletonComponent::class)
abstract class CallBindingsModule {

    @Binds
    abstract fun bindCallNotifier(impl: AndroidCallNotifier): CallNotifier

    @Binds
    abstract fun bindCallAudioController(impl: AndroidCallAudioController): CallAudioController
}

@Module
@InstallIn(SingletonComponent::class)
object CallTeardownModule {

    /** Sign-out ends any live call and clears every call surface. */
    @Provides
    @IntoSet
    fun provideCallTeardown(manager: CallSessionManager): SessionTeardownTask =
        SessionTeardownTask { manager.teardown() }
}
