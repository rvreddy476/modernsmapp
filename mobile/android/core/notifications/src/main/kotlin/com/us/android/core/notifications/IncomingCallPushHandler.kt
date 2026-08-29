package com.us.android.core.notifications

/**
 * The seam a RINGING data-only push crosses into the calling stack
 * (CALL-LB-4). Defined here — where pushes arrive — and IMPLEMENTED by
 * :core:call (which already depends on this module), so the dependency
 * arrow never inverts.
 *
 * The handler must treat the payload as a WAKE-UP, not a fact: the caller
 * identity and call state are re-fetched from the server before anything
 * rings (the same fail-closed rule the socket ring follows).
 */
fun interface IncomingCallPushHandler {
    fun onIncomingCallPush(callId: String, video: Boolean)
}

/** The push types that are a RINGING wake-up rather than a tray notification. */
val RINGING_PUSH_TYPES: Set<String> = setOf("incoming_call", "incoming_video_call")
