package com.us.android.feature.chat.ui

import com.us.android.core.chat.data.ChatSessionManager
import com.us.android.core.notifications.ChatRealtimeLink
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import javax.inject.Provider

/**
 * Binds the push seam to the chat session socket (audit H1): a foreground
 * chat push is dropped only while the socket is [ChatSessionManager.ConnectionState.Connected].
 *
 * A [Provider]: the push presenter is built early (with the messaging
 * service), and the session manager's graph — store, socket, auth — must not
 * be pulled into it, or form a cycle with it, just to answer a state read.
 */
@Module
@InstallIn(SingletonComponent::class)
object ChatRealtimeLinkModule {
    @Provides
    fun provideChatRealtimeLink(session: Provider<ChatSessionManager>): ChatRealtimeLink =
        ChatRealtimeLink { session.get().isLive() }
}

/** True while the session socket is connected: what it delivers, a push need not repeat. */
internal fun ChatSessionManager.isLive(): Boolean =
    connection.value == ChatSessionManager.ConnectionState.Connected
