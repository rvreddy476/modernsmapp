package com.us.android.core.feed.ui.saved

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.feed.data.saved.CollectionNameCheck
import com.us.android.core.feed.data.saved.SavedAction
import com.us.android.core.feed.data.saved.SavedCollection
import com.us.android.core.feed.data.saved.SavedCollectionsRepository
import com.us.android.core.feed.data.saved.canCreateCollection
import com.us.android.core.feed.data.saved.collectionNameCheck
import com.us.android.core.feed.data.saved.savedCollectionRefusal
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** What the picker was opened for. */
sealed interface CollectionTarget {
    val postId: String
    val title: String
    val subtitle: String

    /** "Save to collection" from a post: saves it, or moves it when [alreadySaved]. */
    data class Save(override val postId: String, val alreadySaved: Boolean) : CollectionTarget {
        override val title: String get() = "Save to collection"
        override val subtitle: String get() = "Pick where this post is kept"
    }

    /** "Move to collection" on a saved post that is in [fromCollectionId] now (null = All Saved). */
    data class Move(override val postId: String, val fromCollectionId: String?) : CollectionTarget {
        override val title: String get() = "Move to collection"
        override val subtitle: String get() = "Each saved post is kept in one collection"
    }
}

/** Where the post went: the collection, and the sentence the host shows. */
data class CollectionOutcome(
    val postId: String,
    val fromCollectionId: String?,
    val toCollectionId: String?,
    val collectionName: String,
    val moved: Boolean,
    /** A collection was created on the way. */
    val created: SavedCollection? = null,
) {
    val message: String get() = if (moved) "Moved to $collectionName" else "Saved to $collectionName"
}

data class SaveToCollectionUiState(
    val target: CollectionTarget? = null,
    val loading: Boolean = true,
    val loadError: String? = null,
    val collections: List<SavedCollection> = emptyList(),
    val busy: Boolean = false,
    val error: String? = null,
    /** "New collection" is open: the name field and Create. */
    val naming: Boolean = false,
    val name: String = "",
    val nameProblem: String? = null,
    val outcome: CollectionOutcome? = null,
) {
    val moving: Boolean get() = target is CollectionTarget.Move

    val canCreate: Boolean get() = canCreateCollection(collections)

    /** The collection a saved post is in now, on a move: marked, and not a choice. */
    fun isCurrent(collection: SavedCollection): Boolean =
        (target as? CollectionTarget.Move)?.let { it.fromCollectionId == collection.id } == true
}

/**
 * Behind [SaveToCollectionSheet]. A pick goes to the server and waits: the
 * sheet says where the post is only once it is there. A refusal stays on the
 * sheet, in a sentence, with the choices still open.
 */
@HiltViewModel
class SaveToCollectionViewModel @Inject constructor(
    private val repository: SavedCollectionsRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(SaveToCollectionUiState())
    val state: StateFlow<SaveToCollectionUiState> = _state.asStateFlow()

    fun bind(target: CollectionTarget) {
        if (_state.value.target == target) return
        _state.value = SaveToCollectionUiState(target = target)
        load()
    }

    fun load() {
        _state.update { it.copy(loading = true, loadError = null) }
        viewModelScope.launch {
            when (val result = repository.collections()) {
                is AppResult.Success -> _state.update { it.copy(loading = false, collections = result.data) }
                is AppResult.Failure -> _state.update {
                    it.copy(loading = false, loadError = savedCollectionRefusal(result.error, SavedAction.LOAD))
                }
            }
        }
    }

    fun startNaming() = _state.update { it.copy(naming = true, error = null) }

    fun changeName(name: String) = _state.update { it.copy(name = name, nameProblem = null) }

    /** A collection was picked; the current one on a move is not a choice. */
    fun pick(collection: SavedCollection) {
        val now = _state.value
        if (now.busy || now.isCurrent(collection)) return
        send(collection, created = null)
    }

    /** "Create and save": the name is checked here first, then created, then picked. */
    fun createAndPick() {
        val now = _state.value
        if (now.busy) return
        when (val check = collectionNameCheck(now.name, now.collections)) {
            is CollectionNameCheck.Refused -> _state.update { it.copy(nameProblem = check.problem.message) }
            is CollectionNameCheck.Ok -> {
                _state.update { it.copy(busy = true, error = null, nameProblem = null) }
                viewModelScope.launch {
                    when (val result = repository.create(check.name)) {
                        is AppResult.Success -> {
                            _state.update { it.copy(collections = it.collections + result.data, naming = false, name = "") }
                            send(result.data, created = result.data)
                        }
                        is AppResult.Failure -> _state.update {
                            it.copy(busy = false, nameProblem = savedCollectionRefusal(result.error, SavedAction.CREATE))
                        }
                    }
                }
            }
        }
    }

    fun consumeOutcome() = _state.update { it.copy(outcome = null) }

    private fun send(collection: SavedCollection, created: SavedCollection?) {
        val target = _state.value.target ?: return
        _state.update { it.copy(busy = true, error = null) }
        viewModelScope.launch {
            val (result, action) = when (target) {
                is CollectionTarget.Save ->
                    repository.saveTo(target.postId, collection.id, target.alreadySaved) to SavedAction.SAVE
                is CollectionTarget.Move -> repository.move(target.postId, collection.id) to SavedAction.MOVE
            }
            _state.update { current ->
                when (result) {
                    is AppResult.Success -> current.copy(
                        busy = false,
                        outcome = CollectionOutcome(
                            postId = target.postId,
                            fromCollectionId = (target as? CollectionTarget.Move)?.fromCollectionId,
                            toCollectionId = collection.id,
                            collectionName = collection.name,
                            moved = target is CollectionTarget.Move,
                            created = created,
                        ),
                    )
                    is AppResult.Failure -> current.copy(busy = false, error = savedCollectionRefusal(result.error, action))
                }
            }
        }
    }
}
