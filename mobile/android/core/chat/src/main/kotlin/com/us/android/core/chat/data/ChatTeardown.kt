package com.us.android.core.chat.data

import com.us.android.core.chat.lock.ChatLockManager
import com.us.android.core.common.session.SessionTeardownTask
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Sign-out teardown for chat (directive §5.4, CH-LB-6.5): stop the session
 * socket, cancel the outbox worker, wipe every cached conversation/message/
 * pending-send row, and clear the chat lock so the next account inherits
 * neither this account's plaintext cache nor its lock state.
 *
 * Idempotent and exception-safe by the SessionTeardownTask contract.
 */
@Singleton
class ChatTeardown @Inject constructor(
    private val session: ChatSessionManager,
    private val store: ChatStore,
    private val lock: ChatLockManager,
) : SessionTeardownTask {

    override suspend fun onSignOut() {
        // AWAIT the session's whole job tree (socket loop, reconciliation,
        // room subscribes) before wiping — cancellation alone can leave a
        // writer mid-flight (F2-LB-1); the store's write gate then refuses
        // whatever this join could not reach (e.g. a WorkManager worker).
        session.stopAndJoin()
        store.wipeForLogout()
        lock.clearForLogout()
    }
}
