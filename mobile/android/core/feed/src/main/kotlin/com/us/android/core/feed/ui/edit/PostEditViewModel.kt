package com.us.android.core.feed.ui.edit

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.feed.data.FeedCategory
import com.us.android.core.feed.data.PostEditForm
import com.us.android.core.feed.data.PostEditRepository
import com.us.android.core.feed.data.VideoFeedRepository
import com.us.android.core.feed.data.postEditPatch
import com.us.android.core.feed.data.postEditProblems
import com.us.android.core.feed.data.postEditRefusal
import com.us.android.core.feed.data.toEditForm
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** The Edit sheet, as it draws. */
data class PostEditUiState(
    /** The post's owner fields are on their way. */
    val loading: Boolean = true,
    /** The detail could not be read: the sheet offers Retry instead of a form. */
    val loadError: String? = null,
    /** As the sheet opened: what Save diffs against. */
    val base: PostEditForm = PostEditForm(),
    /** As the owner has it now. */
    val form: PostEditForm = PostEditForm(),
    /** A long video edits a few fields a short does not (made for kids, age, altered content), as at upload. */
    val longVideo: Boolean = true,
    val categories: List<FeedCategory> = emptyList(),
    val saving: Boolean = false,
    /** The checks before Save, or the server's refusal. */
    val problems: List<String> = emptyList(),
    /** The save landed; the sheet leaves. */
    val saved: Boolean = false,
) {
    /** Something moved: Save has a patch to send. */
    val dirty: Boolean get() = !postEditPatch(base, form).isEmpty
}

/**
 * Behind [PostEditSheet] (2026-10-21, the web's Edit sheet): reads the
 * post's owner fields, holds the form, and saves ONLY what changed with
 * `PATCH /v1/posts/:id`. A landed save is recorded in `PostEdits`, which
 * every list layers over its rows, so the reel or the video on screen shows
 * the new words at once.
 */
@HiltViewModel
class PostEditViewModel @Inject constructor(
    private val repository: PostEditRepository,
    private val videos: VideoFeedRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(PostEditUiState())
    val state: StateFlow<PostEditUiState> = _state.asStateFlow()

    private var postId = ""

    /**
     * The sheet opened on [id]; [longVideo] picks which fields it offers.
     * Every opening reads the post afresh: a sheet reopened after a save must
     * start from what the server now has, not leave at once on the old "saved".
     */
    fun bind(id: String, longVideo: Boolean) {
        if (id.isBlank()) return
        postId = id
        _state.value = PostEditUiState(longVideo = longVideo)
        load()
        viewModelScope.launch {
            val categories = videos.categories()
            _state.update { it.copy(categories = categories) }
        }
    }

    fun retry() = load()

    private fun load() {
        val id = postId
        _state.update { it.copy(loading = true, loadError = null) }
        viewModelScope.launch {
            _state.update { current ->
                when (val result = repository.detail(id)) {
                    is AppResult.Success -> {
                        val form = result.data.toEditForm()
                        current.copy(loading = false, base = form, form = form)
                    }
                    is AppResult.Failure -> current.copy(loading = false, loadError = postEditRefusal(result.error))
                }
            }
        }
    }

    /** Every field's change goes through here: the sheet hands back the form with one field moved. */
    fun change(transform: (PostEditForm) -> PostEditForm) =
        _state.update { it.copy(form = transform(it.form), problems = emptyList()) }

    fun save() {
        val now = _state.value
        if (now.saving || now.loading) return
        val problems = postEditProblems(now.form)
        if (problems.isNotEmpty()) {
            _state.update { it.copy(problems = problems) }
            return
        }
        val patch = postEditPatch(now.base, now.form)
        if (patch.isEmpty) {
            _state.update { it.copy(saved = true) }
            return
        }
        _state.update { it.copy(saving = true, problems = emptyList()) }
        viewModelScope.launch {
            _state.update { current ->
                when (val result = repository.save(postId, patch)) {
                    is AppResult.Success -> current.copy(saving = false, saved = true, base = current.form)
                    is AppResult.Failure ->
                        current.copy(saving = false, problems = listOf(postEditRefusal(result.error)))
                }
            }
        }
    }
}
