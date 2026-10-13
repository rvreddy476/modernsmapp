package com.us.android.core.notifications

import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.channels.BufferOverflow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.asSharedFlow

/** A refresh hint, never account state. Screens always fetch authoritative data. */
@Singleton
class NotificationUpdates @Inject constructor() {
    private val changes = MutableSharedFlow<String>(extraBufferCapacity = 1, onBufferOverflow = BufferOverflow.DROP_OLDEST)
    val events = changes.asSharedFlow()
    fun received(type: String) { changes.tryEmit(type) }
}
