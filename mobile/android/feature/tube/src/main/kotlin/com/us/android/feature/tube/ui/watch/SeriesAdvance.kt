package com.us.android.feature.tube.ui.watch

import com.us.android.feature.tube.data.SeriesEpisode
import com.us.android.feature.tube.data.SeriesInfo

/**
 * What happens when a video ends (founder, 2026-09-12): a video in a series
 * counts down to the next episode, anything else stops on its end screen.
 * Auto-advance is series-only on purpose: the browse list under a video is
 * a recommendation, and moving the viewer along it without asking is how a
 * watch session turns into one nobody chose.
 */
sealed interface EndOfVideo {

    /** The next episode plays in [seconds] unless the viewer says otherwise. */
    data class Countdown(val next: SeriesEpisode, val seconds: Int) : EndOfVideo

    /** Replay, or pick from what is recommended; nothing plays on its own. */
    data object EndScreen : EndOfVideo
}

/** How long the viewer has to cancel before the next episode plays. */
const val AUTOPLAY_NEXT_SECONDS = 10

/**
 * The episode after the current one, by episode number rather than list
 * position, and tolerant of gaps: a series numbered 1, 2, 4 (an episode
 * unpublished, or hidden from this viewer) goes from 2 to 4 rather than
 * stopping. Null at the last episode, and null when the current post is
 * not in the list at all: a post the list does not know is not "before"
 * anything. The same rule as the web's series links.
 */
fun nextEpisode(episodes: List<SeriesEpisode>, currentId: String): SeriesEpisode? {
    val current = episodes.firstOrNull { it.postId == currentId } ?: return null
    return episodes.filter { it.episodeNum > current.episodeNum }.minByOrNull { it.episodeNum }
}

/** The episode before the current one, under the same rule as [nextEpisode]. */
fun prevEpisode(episodes: List<SeriesEpisode>, currentId: String): SeriesEpisode? {
    val current = episodes.firstOrNull { it.postId == currentId } ?: return null
    return episodes.filter { it.episodeNum < current.episodeNum }.maxByOrNull { it.episodeNum }
}

/**
 * The end screen when there is no series, when this was the last episode,
 * or when the viewer switched autoplay off; the countdown otherwise. The
 * preference is checked here rather than by the caller so there is one
 * place that says what autoplay means.
 */
fun endOfVideo(series: SeriesInfo?, currentId: String, autoplayEnabled: Boolean): EndOfVideo {
    if (!autoplayEnabled || series == null) return EndOfVideo.EndScreen
    val next = nextEpisode(series.episodes, currentId) ?: return EndOfVideo.EndScreen
    return EndOfVideo.Countdown(next, AUTOPLAY_NEXT_SECONDS)
}
