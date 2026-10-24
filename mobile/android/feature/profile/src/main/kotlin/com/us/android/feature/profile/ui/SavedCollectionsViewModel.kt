package com.us.android.feature.profile.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.paging.PagingData
import androidx.paging.cachedIn
import androidx.paging.filter
import com.us.android.core.common.result.AppResult
import com.us.android.core.feed.data.saved.CollectionNameCheck
import com.us.android.core.feed.data.saved.EMPTY_DEFAULT_COLLECTION
import com.us.android.core.feed.data.saved.SavedAction
import com.us.android.core.feed.data.saved.SavedCollection
import com.us.android.core.feed.data.saved.SavedCollectionsRepository
import com.us.android.core.feed.data.saved.SavedPostsSource
import com.us.android.core.feed.data.saved.afterDelete
import com.us.android.core.feed.data.saved.afterMove
import com.us.android.core.feed.data.saved.afterRename
import com.us.android.core.feed.data.saved.canCreateCollection
import com.us.android.core.feed.data.saved.collectionNameCheck
import com.us.android.core.feed.data.saved.collectionRef
import com.us.android.core.feed.data.saved.resolveSelectedCollection
import com.us.android.core.feed.data.saved.savedCollectionRefusal
import com.us.android.core.feed.data.saved.stillShownAfterMove
import com.us.android.core.feed.ui.saved.CollectionOutcome
import com.us.android.core.model.FeedItem
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.drop
import kotlinx.coroutines.flow.flatMapLatest
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** A dialog the Saved tab has open. */
sealed interface SavedDialog {
    /** "New collection": the name field. */
    data class Create(val name: String = "", val problem: String? = null) : SavedDialog

    /** "Rename": the field starts on the current name. */
    data class Rename(val collection: SavedCollection, val name: String, val problem: String? = null) : SavedDialog

    /** "Delete": what happens to its posts, said before it happens. */
    data class ConfirmDelete(val collection: SavedCollection, val problem: String? = null) : SavedDialog
}

data class SavedTabUiState(
    /** All Saved first, then the viewer's collections, oldest first. */
    val collections: List<SavedCollection> = listOf(EMPTY_DEFAULT_COLLECTION),
    /** The collection on screen; null is All Saved. */
    val selected: String? = null,
    val loadError: String? = null,
    val busy: Boolean = false,
    val dialog: SavedDialog? = null,
    /** The last outcome or refusal, one sentence under the collection pills. */
    val notice: String? = null,
) {
    val selectedCollection: SavedCollection
        get() = collections.firstOrNull { it.id == selected } ?: collections.first()

    val canCreate: Boolean get() = canCreateCollection(collections)
}

/**
 * The Saved tab's collections (saved collections, 2026-10-24): the pills —
 * All Saved and each collection, with counts — and the posts of the one on
 * screen (`GET /v1/posts/bookmarks?collection_id=`). New, Rename and Delete
 * for a collection; "Move to collection" for a post, which leaves the
 * collection on screen at once.
 *
 * Asks nothing until [activate]: only the viewer's OWN profile has a Saved
 * tab, and the grid says when it is that profile.
 */
@HiltViewModel
class SavedCollectionsViewModel @Inject constructor(
    private val repository: SavedCollectionsRepository,
    private val source: SavedPostsSource,
) : ViewModel() {

    private val _state = MutableStateFlow(SavedTabUiState())
    val state: StateFlow<SavedTabUiState> = _state.asStateFlow()

    private val active = MutableStateFlow(false)

    /** Posts moved OUT of the collection on screen; they leave its pages without a reload. Reset per collection. */
    private val movedOut = MutableStateFlow<Set<String>>(emptySet())

    @OptIn(ExperimentalCoroutinesApi::class)
    private val pages: Flow<PagingData<FeedItem>> =
        combine(active, _state.map { it.selected }.distinctUntilChanged()) { on, selected -> on to selected }
            .distinctUntilChanged()
            .flatMapLatest { (on, selected) ->
                if (on) source.savedPosts(collectionRef(selected)) else flowOf(PagingData.empty())
            }
            .cachedIn(viewModelScope)

    /** The collection on screen's posts, less the ones just moved out of it. */
    val posts: Flow<PagingData<FeedItem>> =
        combine(pages, movedOut) { page, gone -> if (gone.isEmpty()) page else page.filter { it.id !in gone } }

    /** The own profile's grid is on screen: read the collections, and again whenever a save or a move lands anywhere. */
    fun activate() {
        if (active.value) return
        active.value = true
        load()
        viewModelScope.launch { repository.changes.drop(1).collect { load() } }
    }

    /** Pull to refresh, or Retry. */
    fun load() {
        viewModelScope.launch {
            when (val result = repository.collections()) {
                is AppResult.Success -> _state.update { current ->
                    current.copy(
                        collections = result.data,
                        selected = resolveSelectedCollection(current.selected, result.data),
                        loadError = null,
                    )
                }
                is AppResult.Failure -> _state.update {
                    it.copy(loadError = savedCollectionRefusal(result.error, SavedAction.LOAD))
                }
            }
        }
    }

    fun select(collectionId: String?) {
        val now = _state.value
        val next = resolveSelectedCollection(collectionId, now.collections)
        if (next == now.selected) return
        movedOut.value = emptySet()
        _state.update { it.copy(selected = next, notice = null) }
    }

    fun startCreate() = _state.update { it.copy(dialog = SavedDialog.Create(), notice = null) }

    fun startRename(collection: SavedCollection) {
        if (collection.isDefault) return
        _state.update { it.copy(dialog = SavedDialog.Rename(collection, collection.name), notice = null) }
    }

    fun startDelete(collection: SavedCollection) {
        if (collection.isDefault) return
        _state.update { it.copy(dialog = SavedDialog.ConfirmDelete(collection), notice = null) }
    }

    fun changeDialogName(name: String) = _state.update { state ->
        state.copy(
            dialog = when (val dialog = state.dialog) {
                is SavedDialog.Create -> dialog.copy(name = name, problem = null)
                is SavedDialog.Rename -> dialog.copy(name = name, problem = null)
                else -> dialog
            },
        )
    }

    fun dismissDialog() {
        if (_state.value.busy) return
        _state.update { it.copy(dialog = null) }
    }

    /** The open dialog's action: Create, Rename, or Delete. */
    fun confirmDialog() {
        val now = _state.value
        if (now.busy) return
        when (val dialog = now.dialog) {
            is SavedDialog.Create -> create(dialog, now.collections)
            is SavedDialog.Rename -> rename(dialog, now.collections)
            is SavedDialog.ConfirmDelete -> delete(dialog)
            null -> Unit
        }
    }

    /**
     * A post moved (from the picker): the counts follow at once, and it leaves
     * the collection on screen unless that is where it went.
     */
    fun onMoved(outcome: CollectionOutcome) {
        _state.update { current ->
            val withCreated = outcome.created?.takeIf { made -> current.collections.none { it.id == made.id } }
                ?.let { current.collections + it } ?: current.collections
            current.copy(
                collections = withCreated.afterMove(outcome.fromCollectionId, outcome.toCollectionId),
                notice = outcome.message,
            )
        }
        if (!stillShownAfterMove(_state.value.selected, outcome.toCollectionId)) {
            movedOut.update { it + outcome.postId }
        }
    }

    fun dismissNotice() = _state.update { it.copy(notice = null) }

    private fun create(dialog: SavedDialog.Create, collections: List<SavedCollection>) {
        when (val check = collectionNameCheck(dialog.name, collections)) {
            is CollectionNameCheck.Refused -> refuse(check.problem.message)
            is CollectionNameCheck.Ok -> send {
                when (val result = repository.create(check.name)) {
                    is AppResult.Success -> _state.update { current ->
                        current.copy(
                            collections = current.collections + result.data,
                            dialog = null,
                            notice = "Created ${result.data.name}",
                        )
                    }
                    is AppResult.Failure -> refuse(savedCollectionRefusal(result.error, SavedAction.CREATE))
                }
            }
        }
    }

    private fun rename(dialog: SavedDialog.Rename, collections: List<SavedCollection>) {
        val id = dialog.collection.id ?: return
        when (val check = collectionNameCheck(dialog.name, collections, renamingId = id)) {
            is CollectionNameCheck.Refused -> refuse(check.problem.message)
            is CollectionNameCheck.Ok -> send {
                when (val result = repository.rename(id, check.name)) {
                    is AppResult.Success -> _state.update { current ->
                        current.copy(
                            collections = current.collections.afterRename(result.data),
                            dialog = null,
                            notice = "Renamed to ${result.data.name}",
                        )
                    }
                    is AppResult.Failure -> refuse(savedCollectionRefusal(result.error, SavedAction.RENAME))
                }
            }
        }
    }

    private fun delete(dialog: SavedDialog.ConfirmDelete) {
        val id = dialog.collection.id ?: return
        send {
            when (val result = repository.delete(id)) {
                is AppResult.Success -> {
                    val wasOnScreen = _state.value.selected == id
                    if (wasOnScreen) movedOut.value = emptySet()
                    _state.update { current ->
                        val left = current.collections.afterDelete(id, result.data.movedToDefault)
                        current.copy(
                            collections = left,
                            selected = resolveSelectedCollection(current.selected, left),
                            dialog = null,
                            notice = deletedNotice(dialog.collection.name, result.data.movedToDefault),
                        )
                    }
                }
                is AppResult.Failure -> refuse(savedCollectionRefusal(result.error, SavedAction.DELETE))
            }
        }
    }

    private fun send(block: suspend () -> Unit) {
        _state.update { it.copy(busy = true) }
        viewModelScope.launch {
            try {
                block()
            } finally {
                _state.update { it.copy(busy = false) }
            }
        }
    }

    /** A refusal stays in the open dialog; with no dialog it is the notice. */
    private fun refuse(message: String) = _state.update { state ->
        when (val dialog = state.dialog) {
            is SavedDialog.Create -> state.copy(dialog = dialog.copy(problem = message))
            is SavedDialog.Rename -> state.copy(dialog = dialog.copy(problem = message))
            is SavedDialog.ConfirmDelete -> state.copy(dialog = dialog.copy(problem = message))
            null -> state.copy(notice = message)
        }
    }
}

/** What a delete says: where its posts went. */
internal fun deletedNotice(name: String, moved: Long): String = when (moved) {
    0L -> "Deleted $name"
    1L -> "Deleted $name. Its 1 post is in All Saved now."
    else -> "Deleted $name. Its $moved posts are in All Saved now."
}

/** The delete dialog's sentence, before anything happens. */
internal fun deleteQuestion(collection: SavedCollection): String = when (collection.count) {
    0L -> "This collection is empty."
    1L -> "The 1 post in it moves to All Saved. Nothing is unsaved."
    else -> "The ${collection.count} posts in it move to All Saved. Nothing is unsaved."
}
