package com.us.android.chat

import com.google.common.truth.Truth.assertThat
import com.us.android.core.call.CallEndReason
import com.us.android.core.call.CallState
import org.junit.Test

/** Founder, 23 Oct 2026: online while the app is open, as on WhatsApp. */
class ChatForegroundPresenceTest {

    @Test
    fun `the socket is wanted while open and signed in, or in a call`() {
        assertThat(socketWanted(foreground = true, signedIn = true, inCall = false)).isTrue()
        assertThat(socketWanted(foreground = false, signedIn = true, inCall = false)).isFalse()
        assertThat(socketWanted(foreground = false, signedIn = true, inCall = true)).isTrue()
        assertThat(socketWanted(foreground = true, signedIn = false, inCall = false)).isFalse()
        assertThat(socketWanted(foreground = true, signedIn = false, inCall = true)).isFalse()
    }

    @Test
    fun `only ringing, connecting and live calls hold the socket`() {
        assertThat(CallState.Idle.holdsSocket()).isFalse()
        assertThat(CallState.Ended(CallEndReason.HungUp, "p", video = false, durationMillis = 0).holdsSocket()).isFalse()
        assertThat(CallState.Incoming("c", "i", "p", video = false).holdsSocket()).isTrue()
        assertThat(CallState.Outgoing("c", "p", video = true).holdsSocket()).isTrue()
        assertThat(CallState.Connecting("c", "p", video = false, incoming = true).holdsSocket()).isTrue()
        assertThat(CallState.Active("c", "p", video = false, startedAtMillis = 0).holdsSocket()).isTrue()
    }

    @Test
    fun `the gate starts on open, stops on close, and never stops what it never saw wanted`() {
        val log = mutableListOf<String>()
        val gate = PresenceSocketGate(start = { log += "start" }, stop = { log += "stop" })

        // Cold start in the background (a push-started call may own the socket).
        gate.update(false)
        assertThat(log).isEmpty()

        gate.update(true)
        gate.update(true)
        assertThat(log).containsExactly("start")

        gate.update(false)
        gate.update(false)
        assertThat(log).containsExactly("start", "stop").inOrder()

        gate.update(true)
        assertThat(log).containsExactly("start", "stop", "start").inOrder()
    }
}
