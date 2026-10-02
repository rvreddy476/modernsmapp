package com.us.android.feature.tube.ui.collections

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.navigation.toRoute
import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.feed.data.FollowGraph
import com.us.android.core.feed.data.VideoCollection
import com.us.android.core.feed.data.VideoLibraryRepository
import com.us.android.core.feed.data.VideoLibraryState
import com.us.android.core.feed.data.VideoLibraryStore
import com.us.android.core.feed.data.VideoThumb
import com.us.android.core.feed.data.videoThumb
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.model.FeedItem
import com.us.android.feature.tube.data.TubeQueue
import com.us.android.feature.tube.navigation.TubeCollectionRoute
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** One collection's page: being read, read, or not readable. */
sealed interface CollectionContent {
    data object Loading : CollectionContent
    data class Ready(val collection: VideoCollection, val videos: List<FeedItem>) : CollectionContent
    data class Failed(val message: String) : CollectionContent
}

/**
 * The videos of [content] the viewer still has in the list. In Watch later a
 * video the viewer took out this session (here, or on the watch screen) is
 * gone at once; a collection the viewer made is exactly what was read.
 */
fun visibleVideos(content: CollectionContent.Ready, library: VideoLibraryState): List<FeedItem> =
    if (content.collection.isWatchLater) {
        content.videos.filter { library.queuedOr(it.id, server = true) }
    } else {
        content.videos
    }

/**
 * One collection (2026-10-02): Watch later, or one the viewer made. The list
 * the watch page's "Watch later" and "Add to collection" fill, where a video
 * put there is found again and opened.
 *
 * Read on open and again every time the page comes back to the front, so a
 * video added on the watch screen is there on the way back. Remove is
 * optimistic: the row goes at once and comes back, with a line saying so, if
 * the server refuses.
 */
@HiltViewModel
// Constructor injection of the page's collaborators; a wrapper would add
// indirection, not clarity.
@Suppress("LongParameterList")
class CollectionViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val repository: VideoLibraryRepository,
    private val library: VideoLibraryStore,
    private val urlResolver: MediaUrlResolver,
    private val queue: TubeQueue,
    follows: FollowGraph,
) : ViewModel() {

    private val collectionId = savedStateHandle.toRoute<TubeCollectionRoute>().collectionId
    private val watchLater = collectionId == TubeCollectionRoute.WATCH_LATER

    private val loaded = MutableStateFlow<CollectionContent>(CollectionContent.Loading)

    /** What the page shows: the list as read, less what the viewer has taken out of Watch later since. */
    val content: StateFlow<CollectionContent> = combine(loaded, library.state) { content, state ->
        if (content is CollectionContent.Ready) content.copy(videos = visibleVideos(content, state)) else content
    }.stateIn(viewModelScope, SharingStarted.WhileSubscribed(STOP_TIMEOUT_MILLIS), CollectionContent.Loading)

    private val _message = MutableStateFlow<UsMessage?>(null)
    val message: StateFlow<UsMessage?> = _message.asStateFlow()

    init {
        library.setViewer(follows.ownId)
        refresh()
    }

    /** Reads the list again. The rows on screen stay while it does; only a first read shows the loader. */
    fun refresh() {
        viewModelScope.launch {
            val result = if (watchLater) repository.watchLater() else repository.collection(collectionId)
            loaded.value = when (result) {
                is AppResult.Success -> CollectionContent.Ready(result.data.collection, result.data.videos)
                is AppResult.Failure ->
                    // A refresh that fails keeps what was already read.
                    loaded.value as? CollectionContent.Ready ?: CollectionContent.Failed(
                        VideoLibraryRepository.errorMessage(result.error, "We couldn't load this list."),
                    )
            }
        }
    }

    /** The loader again, then the read: the error state's Retry. */
    fun retry() {
        loaded.value = CollectionContent.Loading
        refresh()
    }

    /** Takes [item] out of the list. The row goes at once; a refusal puts it back and says so. */
    fun remove(item: FeedItem) {
        val before = loaded.value as? CollectionContent.Ready ?: return
        viewModelScope.launch {
            val result = if (watchLater) {
                library.setQueued(item.id, queued = false, serverQueued = true)
            } else {
                loaded.update { current ->
                    if (current is CollectionContent.Ready) {
                        current.copy(videos = current.videos.filterNot { it.id == item.id })
                    } else {
                        current
                    }
                }
                repository.removeFromCollection(collectionId, item.id)
            }
            if (result is AppResult.Failure) {
                if (!watchLater) loaded.value = before
                _message.value = UsMessage(
                    text = VideoLibraryRepository.errorMessage(result.error, "Couldn't remove that. Try again."),
                    type = UsMessageType.Error,
                )
            }
        }
    }

    fun dismissMessage() {
        _message.value = null
    }

    fun thumb(item: FeedItem): VideoThumb = urlResolver.videoThumb(item)

    /** A row was tapped: the list becomes the watch screen's queue, so "Up next" is the rest of it. */
    fun onOpen(videos: List<FeedItem>) = queue.set(videos)

    private companion object {
        const val STOP_TIMEOUT_MILLIS = 5_000L
    }
}

/** What the collections index shows. */
sealed interface CollectionsContent {
    data object Loading : CollectionsContent
    data class Ready(val collections: List<VideoCollection>) : CollectionsContent
    data class Failed(val message: String) : CollectionsContent
}

/**
 * "Collections" (2026-10-02): the lists the viewer made, each opening its
 * page. Watch later is not among them: it has its own row in Tube's menu, as
 * the web keeps it apart.
 */
@HiltViewModel
class CollectionsViewModel @Inject constructor(
    private val repository: VideoLibraryRepository,
    private val follows: FollowGraph,
) : ViewModel() {

    private val _content = MutableStateFlow<CollectionsContent>(CollectionsContent.Loading)
    val content: StateFlow<CollectionsContent> = _content.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        viewModelScope.launch {
            _content.value = when (val result = repository.collections(follows.ownId)) {
                is AppResult.Success -> CollectionsContent.Ready(result.data)
                is AppResult.Failure ->
                    _content.value as? CollectionsContent.Ready ?: CollectionsContent.Failed(
                        VideoLibraryRepository.errorMessage(result.error, "We couldn't load your collections."),
                    )
            }
        }
    }

    fun retry() {
        _content.value = CollectionsContent.Loading
        refresh()
    }
}
