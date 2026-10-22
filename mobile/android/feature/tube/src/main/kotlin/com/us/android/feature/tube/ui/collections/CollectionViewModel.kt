package com.us.android.feature.tube.ui.collections

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.navigation.toRoute
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.feed.data.CollectionEdit
import com.us.android.core.feed.data.CollectionVisibility
import com.us.android.core.feed.data.FollowGraph
import com.us.android.core.feed.data.VideoCollection
import com.us.android.core.feed.data.VideoCollectionSource
import com.us.android.core.feed.data.VideoLibraryRepository
import com.us.android.core.feed.data.VideoLibraryState
import com.us.android.core.feed.data.VideoLibraryStore
import com.us.android.core.feed.data.VideoThumb
import com.us.android.core.feed.data.planCollectionMove
import com.us.android.core.feed.data.reconcileCollectionOrder
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

    /** [order] is every row's post id, hidden rows included: what a reorder is planned against. */
    data class Ready(
        val collection: VideoCollection,
        val videos: List<FeedItem>,
        val order: List<String> = videos.map { it.id },
    ) : CollectionContent

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

/** What a collection's page may offer, by whose list it is (web `CollectionView`). */
data class CollectionPermissions(
    /** Rename, re-describe, change who can see it, delete: a list the viewer made, never a system one. */
    val canEdit: Boolean,
    /** Move up / down: the same lists (a system list refuses `409 SYSTEM_PLAYLIST`). */
    val canReorder: Boolean,
    /** The × on a row: Watch later (its own route) and a list the viewer made. Liked videos is written by the like alone. */
    val canRemove: Boolean,
)

/** Pure, so it is a table test. */
fun collectionPermissions(collection: VideoCollection): CollectionPermissions = CollectionPermissions(
    canEdit = !collection.isSystem,
    canReorder = !collection.isSystem,
    canRemove = !collection.isLiked,
)

/** The details sheet's progress: closed, open, or saving. */
enum class CollectionEditState { CLOSED, OPEN, SAVING }

/**
 * One collection (2026-10-02): Watch later, Liked videos (2026-10-22), or
 * one the viewer made. The list the watch page's "Watch later" and "Add to
 * collection" fill, where a video put there is found again and opened.
 *
 * Read on open and again every time the page comes back to the front, so a
 * video added on the watch screen is there on the way back. Every write is
 * optimistic: Remove takes the row out at once, a move reorders at once,
 * and either comes back, with a line saying so, if the server refuses. The
 * details edit and Delete (web parity, 2026-10-22) wait for the server: a
 * renamed list that had to be renamed back, or a deleted one restored, is
 * worse than a short wait.
 */
@HiltViewModel
// Constructor injection of the page's collaborators; a wrapper would add
// indirection, not clarity. One function per thing the page can ask.
@Suppress("LongParameterList", "TooManyFunctions")
class CollectionViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val source: VideoCollectionSource,
    private val library: VideoLibraryStore,
    private val urlResolver: MediaUrlResolver,
    private val queue: TubeQueue,
    follows: FollowGraph,
) : ViewModel() {

    private val collectionId = savedStateHandle.toRoute<TubeCollectionRoute>().collectionId
    private val watchLater = collectionId == TubeCollectionRoute.WATCH_LATER
    private val liked = collectionId == TubeCollectionRoute.LIKED

    private val loaded = MutableStateFlow<CollectionContent>(CollectionContent.Loading)

    /** What the page shows: the list as read, less what the viewer has taken out of Watch later since. */
    val content: StateFlow<CollectionContent> = combine(loaded, library.state) { content, state ->
        if (content is CollectionContent.Ready) content.copy(videos = visibleVideos(content, state)) else content
    }.stateIn(viewModelScope, SharingStarted.WhileSubscribed(STOP_TIMEOUT_MILLIS), CollectionContent.Loading)

    private val _message = MutableStateFlow<UsMessage?>(null)
    val message: StateFlow<UsMessage?> = _message.asStateFlow()

    private val _refreshing = MutableStateFlow(false)

    /** A read in flight over rows already on screen (pull to refresh); never true for the first read. */
    val refreshing: StateFlow<Boolean> = _refreshing.asStateFlow()

    private val _edit = MutableStateFlow(CollectionEditState.CLOSED)

    /** The details sheet (title, description, visibility). */
    val edit: StateFlow<CollectionEditState> = _edit.asStateFlow()

    private val _confirmingDelete = MutableStateFlow(false)
    val confirmingDelete: StateFlow<Boolean> = _confirmingDelete.asStateFlow()

    private val _deleting = MutableStateFlow(false)
    val deleting: StateFlow<Boolean> = _deleting.asStateFlow()

    private val _deleted = MutableStateFlow(false)

    /** True once the server deleted the collection: the page leaves. */
    val deleted: StateFlow<Boolean> = _deleted.asStateFlow()

    /** True while a move is on the wire; the next waits, so two moves never race their answers. */
    private var moving = false

    init {
        library.setViewer(follows.ownId)
        refresh()
    }

    /** Reads the list again. The rows on screen stay while it does; only a first read shows the loader. */
    fun refresh() {
        viewModelScope.launch {
            _refreshing.value = loaded.value is CollectionContent.Ready
            val result = when {
                watchLater -> source.watchLater()
                liked -> source.liked()
                else -> source.collection(collectionId)
            }
            _refreshing.value = false
            loaded.value = when (result) {
                is AppResult.Success ->
                    CollectionContent.Ready(result.data.collection, result.data.videos, result.data.order)
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
        if (!collectionPermissions(before.collection).canRemove) return
        viewModelScope.launch {
            val result = if (watchLater) {
                library.setQueued(item.id, queued = false, serverQueued = true)
            } else {
                loaded.update { current ->
                    if (current is CollectionContent.Ready) {
                        current.copy(
                            videos = current.videos.filterNot { it.id == item.id },
                            order = current.order.filterNot { it == item.id },
                        )
                    } else {
                        current
                    }
                }
                source.removeFromCollection(collectionId, item.id)
            }
            if (result is AppResult.Failure) {
                if (!watchLater) loaded.value = before
                say(VideoLibraryRepository.errorMessage(result.error, "Couldn't remove that. Try again."))
            }
        }
    }

    /**
     * Moves [item] to [toIndex] among the videos shown (Move up / Move down
     * / to the top / to the bottom). The rows reorder at once; the server's
     * answer then sets the order, and a refusal puts the old one back.
     */
    fun move(item: FeedItem, toIndex: Int) {
        val before = loaded.value as? CollectionContent.Ready ?: return
        if (moving || !collectionPermissions(before.collection).canReorder) return
        val plan = planCollectionMove(before.order, before.videos, item.id, toIndex) ?: return
        moving = true
        loaded.value = before.copy(videos = plan.videos, order = plan.order)
        viewModelScope.launch {
            val result = source.moveInCollection(collectionId, item.id, plan.position)
            moving = false
            when (result) {
                is AppResult.Success -> loaded.update { current ->
                    if (current is CollectionContent.Ready) {
                        current.copy(videos = reconcileCollectionOrder(result.data, current.videos), order = result.data)
                    } else {
                        current
                    }
                }
                is AppResult.Failure -> {
                    loaded.value = before
                    say(VideoLibraryRepository.errorMessage(result.error, "Couldn't move that. Try again."))
                }
            }
        }
    }

    fun openEdit() {
        val ready = loaded.value as? CollectionContent.Ready ?: return
        if (collectionPermissions(ready.collection).canEdit) _edit.value = CollectionEditState.OPEN
    }

    fun closeEdit() {
        if (_edit.value != CollectionEditState.SAVING) _edit.value = CollectionEditState.CLOSED
    }

    /**
     * Saves the details sheet. Only what changed is sent; nothing changed
     * closes the sheet without a request. A refusal keeps the sheet open,
     * with what was typed, and says why.
     */
    fun save(title: String, description: String, visibility: CollectionVisibility) {
        val ready = loaded.value as? CollectionContent.Ready ?: return
        if (_edit.value == CollectionEditState.SAVING) return
        val edit = collectionEditOf(ready.collection, title, description, visibility)
        if (edit.isEmpty) {
            _edit.value = CollectionEditState.CLOSED
            return
        }
        _edit.value = CollectionEditState.SAVING
        viewModelScope.launch {
            when (val result = source.updateCollection(collectionId, edit)) {
                is AppResult.Success -> {
                    loaded.update { current ->
                        if (current is CollectionContent.Ready) current.copy(collection = result.data) else current
                    }
                    _edit.value = CollectionEditState.CLOSED
                    _message.value = UsMessage("Collection updated", UsMessageType.Success)
                }
                is AppResult.Failure -> {
                    _edit.value = CollectionEditState.OPEN
                    say(editFailure(result.error))
                }
            }
        }
    }

    fun askDelete() {
        val ready = loaded.value as? CollectionContent.Ready ?: return
        if (collectionPermissions(ready.collection).canEdit) _confirmingDelete.value = true
    }

    fun cancelDelete() {
        _confirmingDelete.value = false
    }

    /** Deletes the collection (its videos stay where they are). The page leaves once the server agrees. */
    fun delete() {
        val ready = loaded.value as? CollectionContent.Ready ?: return
        if (_deleting.value || !collectionPermissions(ready.collection).canEdit) return
        _confirmingDelete.value = false
        _deleting.value = true
        viewModelScope.launch {
            val result = source.deleteCollection(collectionId)
            _deleting.value = false
            when (result) {
                is AppResult.Success -> _deleted.value = true
                // Already gone is gone: the page leaves either way.
                is AppResult.Failure -> if (result.error is AppError.NotFound) {
                    _deleted.value = true
                } else {
                    say(VideoLibraryRepository.errorMessage(result.error, "Couldn't delete this collection. Try again."))
                }
            }
        }
    }

    fun dismissMessage() {
        _message.value = null
    }

    fun thumb(item: FeedItem): VideoThumb = urlResolver.videoThumb(item)

    /** A row was tapped: the list becomes the watch screen's queue, so "Up next" is the rest of it. */
    fun onOpen(videos: List<FeedItem>) = queue.set(videos)

    private fun say(text: String) {
        _message.value = UsMessage(text = text, type = UsMessageType.Error)
    }

    private fun editFailure(error: AppError): String = when (error) {
        is AppError.InvalidRequest -> "A collection needs a title."
        else -> VideoLibraryRepository.errorMessage(error, "Couldn't save your changes. Try again.")
    }

    private companion object {
        const val STOP_TIMEOUT_MILLIS = 5_000L
    }
}

/**
 * What the details sheet changed, as the PATCH should carry it: a trimmed
 * title that differs (a blank one is never sent), a trimmed description
 * that differs, a visibility that differs. Pure, so it is a table test.
 */
fun collectionEditOf(
    current: VideoCollection,
    title: String,
    description: String,
    visibility: CollectionVisibility,
): CollectionEdit {
    val newTitle = title.trim()
    val newDescription = description.trim()
    return CollectionEdit(
        title = newTitle.takeIf { it.isNotEmpty() && it != current.title },
        description = newDescription.takeIf { it != current.description },
        visibility = visibility.takeIf { it != current.visibility },
    )
}

/** Collection title length, as the web's field caps it. */
const val COLLECTION_TITLE_MAX = 120

/** Collection description length, as the web's field caps it. */
const val COLLECTION_DESCRIPTION_MAX = 1_000

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

    private val _refreshing = MutableStateFlow(false)

    /** A read in flight over rows already on screen (pull to refresh); never true for the first read. */
    val refreshing: StateFlow<Boolean> = _refreshing.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        viewModelScope.launch {
            _refreshing.value = _content.value is CollectionsContent.Ready
            val result = repository.collections(follows.ownId)
            _refreshing.value = false
            _content.value = when (result) {
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
