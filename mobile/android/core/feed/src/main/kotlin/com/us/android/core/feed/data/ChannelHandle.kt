package com.us.android.core.feed.data

import java.util.Locale

/**
 * The handle rules, client-side, so the form can say "too short" before the
 * server does. The SERVER is the authority — a handle that passes here can
 * still be taken or refused — and these are deliberately no stricter than
 * the contract: lowercase letters, digits, `_` and `.`, 3 to 30 characters,
 * no leading `@` (it is shown, never stored).
 */
object ChannelHandle {
    const val MIN_LENGTH = 3
    const val MAX_LENGTH = 30

    private val allowed = Regex("[a-z0-9_.]")

    /**
     * What the user typed, as the handle it would be: lowercased, the `@`
     * they may have typed dropped, every other character removed, cut at
     * the cap. The field applies this on every keystroke, so a typed
     * "@Ada Lovelace!" reads "adalovelace" as they type.
     */
    fun normalize(input: String): String =
        input.lowercase(Locale.ROOT)
            .removePrefix("@")
            .filter { allowed.matches(it.toString()) }
            .take(MAX_LENGTH)

    /** Null when [handle] (already normalized) is well-formed; the reason otherwise. */
    fun problem(handle: String): String? = when {
        handle.length < MIN_LENGTH -> "At least $MIN_LENGTH characters"
        handle.startsWith(".") || handle.endsWith(".") -> "Can't start or end with a dot"
        else -> null
    }

    fun isValid(handle: String): Boolean = problem(handle) == null

    /**
     * A first suggestion from what the app already knows about the person:
     * their username when they have one, else their display name squeezed
     * into the rules. The live availability check replaces it with the
     * server's own suggestion when it is taken.
     */
    fun suggest(username: String?, displayName: String): String {
        val fromUsername = username?.let(::normalize).orEmpty()
        if (isValid(fromUsername)) return fromUsername
        val fromName = normalize(displayName.filterNot { it.isWhitespace() })
        return if (isValid(fromName)) fromName else ""
    }
}

/** The channel name rules: 1 to 50 characters after trimming. Mirrors the server's cap for the counter. */
object ChannelName {
    const val MAX_LENGTH = 50

    fun problem(name: String): String? = when {
        name.isBlank() -> "Give your channel a name"
        name.trim().length > MAX_LENGTH -> "At most $MAX_LENGTH characters"
        else -> null
    }
}

/** The About text: optional, up to 500 characters. */
object ChannelAbout {
    const val MAX_LENGTH = 500

    fun problem(about: String): String? =
        if (about.length > MAX_LENGTH) "At most $MAX_LENGTH characters" else null
}

/**
 * What Create → Video does first, from what the client knows about the
 * viewer's channel (founder, 2026-09-05: "channel before video"). Pure, so
 * the decision is a table test.
 */
sealed interface ChannelGate {
    /** There is a channel: straight to the form. */
    data object Proceed : ChannelGate

    /** The server said there is none: the "Create your channel" sheet first. */
    data object CreateFirst : ChannelGate

    /** Not known yet: hold the form behind a loader rather than guessing. */
    data object Wait : ChannelGate

    /** The lookup failed for a reason other than "none": say so, offer Retry. */
    data class Blocked(val message: String) : ChannelGate
}

fun channelGate(state: ChannelState): ChannelGate = when (state) {
    is ChannelState.Present -> ChannelGate.Proceed
    ChannelState.None -> ChannelGate.CreateFirst
    ChannelState.Unknown -> ChannelGate.Wait
    is ChannelState.Failed -> ChannelGate.Blocked(state.message)
}
