package com.us.android.chat

import com.us.android.core.auth.SessionManager
import com.us.android.core.call.CallSessionManager
import com.us.android.core.call.CallState
import com.us.android.core.chat.data.ChatSessionManager
import dagger.Lazy
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.launch
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Online means "the app is open" (founder, 23 Oct 2026: "when user online no
 * green mark").
 *
 * The gateway counts a person online while they hold any live socket. On
 * Android the session socket used to open only when a chat screen did, and
 * then stay open until sign-out — so someone scrolling the feed showed as
 * offline, and someone who had put the phone away showed as online. This
 * ties the socket to the PROCESS being in the foreground, as WhatsApp does:
 *
 *  - foreground + signed in      -> socket on (the person is online)
 *  - background                  -> socket off (last seen = now), EXCEPT
 *  - a call is ringing or live   -> socket stays on: signaling rides it
 *
 * Messages that arrive in the background still come by push, and the push
 * path acknowledges delivery on its own (ChatPushDeliveredHook), so ticks do
 * not depend on the socket staying open. Sign-out is still ChatTeardown's.
 */
@Singleton
class ChatForegroundPresence @Inject constructor(
    private val sessionManager: SessionManager,
    // Lazy: built on the collector's thread, off Application.onCreate.
    private val chatSession: Lazy<ChatSessionManager>,
    private val calls: Lazy<CallSessionManager>,
) {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private val foreground = MutableStateFlow(false)
    private val gate = PresenceSocketGate(start = { chatSession.get().start() }, stop = { chatSession.get().stop() })

    fun start() {
        scope.launch {
            combine(foreground, sessionManager.state, calls.get().state) { fg, session, call ->
                socketWanted(foreground = fg, signedIn = session.isAuthenticated, inCall = call.holdsSocket())
            }
                .distinctUntilChanged()
                .collect(gate::update)
        }
    }

    fun onForeground() {
        foreground.value = true
    }

    fun onBackground() {
        foreground.value = false
    }
}

/** Whether the chat socket should be open (see [ChatForegroundPresence]). */
internal fun socketWanted(foreground: Boolean, signedIn: Boolean, inCall: Boolean): Boolean =
    signedIn && (foreground || inCall)

/** A ringing, connecting or live call needs the socket for signaling. */
internal fun CallState.holdsSocket(): Boolean = this !is CallState.Idle && this !is CallState.Ended

/**
 * Acts on TRANSITIONS only. The first answer at cold start is usually "not
 * wanted" (the process starts in the background before an activity shows);
 * acting on it would stop a socket a push-started incoming call has just
 * opened. So it starts on not-wanted -> wanted, and stops only on wanted ->
 * not-wanted — never a socket it did not see wanted.
 */
internal class PresenceSocketGate(
    private val start: () -> Unit,
    private val stop: () -> Unit,
) {
    private var wanted: Boolean? = null

    fun update(next: Boolean) {
        val previous = wanted
        wanted = next
        when {
            next && previous != true -> start()
            !next && previous == true -> stop()
        }
    }
}
