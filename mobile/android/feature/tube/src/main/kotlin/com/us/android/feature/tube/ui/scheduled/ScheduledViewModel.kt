package com.us.android.feature.tube.ui.scheduled

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.feed.data.VideoFeedRepository
import com.us.android.core.feed.data.videoThumb
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.media.publish.ScheduleWindow
import com.us.android.core.model.FeedItem
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.time.Instant
import java.time.ZoneId
import javax.inject.Inject

/**
 * The scheduled list (2026-09-05): `GET v1/posts/me/scheduled`, soonest
 * first, and the two things a row can do — move its instant, or publish
 * it now — both `PATCH v1/posts/{id}/schedule`. The server answers with
 * the post as it now stands, and that answer is what the row becomes: a
 * post that went live leaves the list, a moved one shows its new time.
 * Nothing here is guessed ahead of the server.
 */
@HiltViewModel
class ScheduledViewModel @Inject constructor(
    private val videos: VideoFeedRepository,
    private val urlResolver: MediaUrlResolver,
) : ViewModel() {

    /** Null while loading; empty when nothing is waiting. */
    private val _items = MutableStateFlow<List<FeedItem>?>(null)
    val items: StateFlow<List<FeedItem>?> = _items.asStateFlow()

    /** The ids with a PATCH in flight — their buttons show a spinner and take no second tap. */
    private val _busy = MutableStateFlow<Set<String>>(emptySet())
    val busy: StateFlow<Set<String>> = _busy.asStateFlow()

    private val _message = MutableStateFlow<UsMessage?>(null)
    val message: StateFlow<UsMessage?> = _message.asStateFlow()

    init {
        load()
    }

    fun load() {
        viewModelScope.launch { _items.value = videos.scheduledPosts(PAGE) }
    }

    /** The row's still: the chosen cover or the video's own thumbnail, else the first image, else nothing. */
    fun still(item: FeedItem): String? =
        urlResolver.videoThumb(item).url
            ?: item.media.firstOrNull { it.kind == IMAGE_KIND }?.let { urlResolver.bestVariant(it.variants, STILL_MAX) }

    /** Moves [item] to [at]. */
    fun reschedule(item: FeedItem, at: Instant) {
        val label = ScheduleWindow.label(at, ZoneId.systemDefault())
        patch(item, ScheduleWindow.wire(at), "Moved to $label.")
    }

    /** Publishes [item] now — an absent `publish_at`. */
    fun publishNow(item: FeedItem) = patch(item, null, "Published.")

    fun dismissMessage() {
        _message.value = null
    }

    private fun patch(item: FeedItem, publishAt: String?, onDone: String) {
        if (item.id in _busy.value) return
        _busy.update { it + item.id }
        viewModelScope.launch {
            when (val result = videos.reschedule(item.id, publishAt)) {
                is AppResult.Success -> {
                    _items.update { list -> list?.replaced(item.id, result.data) }
                    _message.value = UsMessage(onDone, UsMessageType.Success)
                }
                is AppResult.Failure ->
                    _message.value = UsMessage(VideoFeedRepository.scheduleErrorMessage(result.error))
            }
            _busy.update { it - item.id }
        }
    }

    /** The server's answer takes the row's place — or the row leaves, when the post is live now — soonest first. */
    private fun List<FeedItem>.replaced(id: String, updated: FeedItem): List<FeedItem> {
        val live = !updated.isScheduled && updated.publishAt.isNullOrBlank()
        return mapNotNull { row ->
            when {
                row.id != id -> row
                live -> null
                else -> updated
            }
        }.sortedBy { it.publishAt.orEmpty() }
    }

    private companion object {
        const val PAGE = 50
        const val IMAGE_KIND = "image"
        const val STILL_MAX = 360
    }
}
