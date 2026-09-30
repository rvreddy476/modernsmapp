package com.us.android.core.feed.ui.sound

import androidx.lifecycle.ViewModel
import com.us.android.core.media.sound.ReelSoundPlayer
import com.us.android.core.media.sound.SoundPreview
import com.us.android.core.media.sound.SoundPreviewPlayer
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.StateFlow
import javax.inject.Inject

/**
 * Holds the Reels screen's sound player (original sounds, 2026-09-30).
 *
 * A ViewModel of its own, with nothing in it but the player, for two
 * reasons. The screen's own ViewModel stays free of players — a player is an
 * Android object, and the rules that ViewModel holds are tested without one.
 * And the player is let go when the destination is: the screen releases it
 * when it leaves composition, as it does the pool, and this is the backstop
 * for a destination that is destroyed without ever being disposed.
 *
 * One per screen, never shared: two screens in one transition would
 * otherwise release each other's player.
 */
@HiltViewModel
class ReelSoundViewModel @Inject constructor(
    val player: ReelSoundPlayer,
) : ViewModel() {
    override fun onCleared() = player.release()
}

/**
 * Holds a preview player: the play button of the sound page, and of the reel
 * form's Sound section. Shared by the two features through this module,
 * because features must not depend on each other; each screen gets its own
 * instance.
 */
@HiltViewModel
class SoundPreviewViewModel @Inject constructor(
    val player: SoundPreviewPlayer,
) : ViewModel() {
    /** What the button shows. */
    val state: StateFlow<SoundPreview> = player.state

    override fun onCleared() = player.release()
}
