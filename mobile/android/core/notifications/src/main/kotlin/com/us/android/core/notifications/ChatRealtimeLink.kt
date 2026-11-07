package com.us.android.core.notifications

/**
 * Whether the chat session socket is connected right now (audit H1). A
 * foreground chat push may be dropped ONLY then: the socket is what delivers
 * the same message to the open surfaces. While it is down — a cold start
 * before the first token refresh, a weak network, a rejected credential —
 * the push is the only signal the user gets, so it must be shown.
 *
 * Defined here and bound by `:feature:chat`, like [ChatPushDeliveredHook], so
 * the dependency arrow never inverts. Optional: an app without chat binds
 * nothing, and with nothing bound no chat push is ever suppressed.
 *
 * Must answer at once (it reads a state value; no I/O).
 */
fun interface ChatRealtimeLink {
    fun isLive(): Boolean
}

/** Optional: only an app that ships chat binds a [ChatRealtimeLink]. */
@dagger.Module
@dagger.hilt.InstallIn(dagger.hilt.components.SingletonComponent::class)
abstract class ChatRealtimeLinkModule {
    @dagger.BindsOptionalOf
    abstract fun optionalChatRealtimeLink(): ChatRealtimeLink
}
