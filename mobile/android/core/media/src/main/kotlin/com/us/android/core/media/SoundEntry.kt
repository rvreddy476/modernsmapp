package com.us.android.core.media

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The sound a new reel should be made with, as the create flow holds it: what
 * it is called and how long it is, and the id the reel is created with. No
 * URL — the bytes are asked for by id — and no start: there is no offset
 * picker, so a chosen sound starts at 0.
 */
data class ChosenSound(
    val id: String,
    val title: String,
    val artist: String = "",
    val durationMs: Long = 0L,
)

/**
 * The sound "Use this sound" chose, on its way to the reel create flow
 * (original sounds, 2026-09-30).
 *
 * "Use this sound" is pressed in Reels or on the sound page
 * (`:feature:feed`); the reel form is `:feature:post`. Features must not
 * depend on each other and the Create route carries one token — which
 * surface to open — so the sound travels through this holder instead: the
 * feed chooses it, `:app` opens the reel create flow, and the form takes it
 * when it starts. The same shape as [ReelsEntry], which carries a post id
 * the other way.
 *
 * It lives in `:core:media` because both features can see this module. It
 * holds one sound and no logic. [take] hands it over AND clears it, so a
 * later visit to the create flow from the "+" opens without a sound.
 */
@Singleton
class SoundEntry @Inject constructor() {

    private val _chosen = MutableStateFlow<ChosenSound?>(null)

    /** The sound waiting for the create flow, or null when none was chosen. */
    val chosen: StateFlow<ChosenSound?> = _chosen.asStateFlow()

    /** "Use this sound": the next reel create flow opens with [sound] chosen. A blank id is no sound. */
    fun choose(sound: ChosenSound) {
        _chosen.value = sound.takeIf { it.id.isNotBlank() }
    }

    /** The create flow has started: it takes the sound, and the next visit is an ordinary one. */
    fun take(): ChosenSound? = _chosen.value.also { _chosen.value = null }

    /** Nothing is waiting any more. */
    fun clear() {
        _chosen.value = null
    }
}
