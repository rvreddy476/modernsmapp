package com.us.android.feature.tube.ui.collections

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.feed.data.FollowGraph
import com.us.android.core.feed.data.VideoCollection
import com.us.android.core.feed.data.VideoLibraryRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** What the "Add to collection" sheet shows for one video. */
data class CollectionPickerState(
    /** The viewer's collections are being read. */
    val loading: Boolean = true,
    val collections: List<VideoCollection> = emptyList(),
    /** The collections the video went into while the sheet was open: their rows show a check. */
    val added: Set<String> = emptySet(),
    /** An add or a create is on the wire: the rows wait. */
    val busy: Boolean = false,
    /** The last refusal, read under the rows; null when there is none. */
    val error: String? = null,
)

/**
 * "Add to collection" (2026-10-02): the viewer's collections, one tap to add
 * the video to one, and a quick create for a new one. The web's "Save to
 * playlist" dialog, with RUTUBE's word for the thing.
 *
 * Nothing here is optimistic: a row shows its check only once the server
 * has the video, because a check that came off again would read as a
 * collection that lost it.
 *
 * A plain class, not the ViewModel: it holds no scope and no Android object,
 * so each step is a suspend call a test can await.
 */
class CollectionPicker @Inject constructor(private val library: VideoLibraryRepository) {

    private val _state = MutableStateFlow(CollectionPickerState())
    val state: StateFlow<CollectionPickerState> = _state.asStateFlow()

    private var postId: String = ""

    /** The sheet opened on a video: nothing from the last one carries over, and [ownerId]'s list is read again. */
    suspend fun open(postId: String, ownerId: String) {
        this.postId = postId
        _state.value = CollectionPickerState()
        _state.value = when (val result = library.collections(ownerId)) {
            is AppResult.Success -> CollectionPickerState(loading = false, collections = result.data)
            is AppResult.Failure -> CollectionPickerState(
                loading = false,
                error = VideoLibraryRepository.errorMessage(result.error, "Couldn't load your collections."),
            )
        }
    }

    /** Adds the video to [collection]. A second tap on a collection that already has it does nothing. */
    suspend fun add(collection: VideoCollection) {
        if (!begin { collection.id !in it.added }) return
        addTo(collection)
    }

    /** Makes a collection called [title] and puts the video in it. A blank title is not sent. */
    suspend fun create(title: String, isPrivate: Boolean) {
        val name = title.trim()
        if (name.isEmpty() || !begin { true }) return
        when (val created = library.createCollection(name, isPrivate)) {
            is AppResult.Success -> {
                _state.update { it.copy(collections = listOf(created.data) + it.collections) }
                addTo(created.data)
            }
            is AppResult.Failure -> _state.update {
                it.copy(
                    busy = false,
                    error = VideoLibraryRepository.errorMessage(created.error, "Couldn't create the collection."),
                )
            }
        }
    }

    /** Takes the one in-flight slot if it is free and [allowed]; a tap while a write is out is dropped. */
    private fun begin(allowed: (CollectionPickerState) -> Boolean): Boolean {
        val current = _state.value
        if (current.busy || !allowed(current)) return false
        _state.update { it.copy(busy = true, error = null) }
        return true
    }

    private suspend fun addTo(collection: VideoCollection) {
        val result = library.addToCollection(collection.id, postId)
        _state.update { state ->
            when (result) {
                is AppResult.Success -> state.copy(busy = false, added = state.added + collection.id)
                is AppResult.Failure -> state.copy(
                    busy = false,
                    error = VideoLibraryRepository.errorMessage(result.error, "Couldn't add to ${collection.title}."),
                )
            }
        }
    }
}

/** The sheet's ViewModel: [CollectionPicker] on the screen's scope, for the signed-in viewer. */
@HiltViewModel
class CollectionPickerViewModel @Inject constructor(
    private val picker: CollectionPicker,
    private val follows: FollowGraph,
) : ViewModel() {

    val state: StateFlow<CollectionPickerState> = picker.state

    fun open(postId: String) {
        viewModelScope.launch { picker.open(postId, follows.ownId) }
    }

    fun add(collection: VideoCollection) {
        viewModelScope.launch { picker.add(collection) }
    }

    fun create(title: String, isPrivate: Boolean) {
        viewModelScope.launch { picker.create(title, isPrivate) }
    }
}
