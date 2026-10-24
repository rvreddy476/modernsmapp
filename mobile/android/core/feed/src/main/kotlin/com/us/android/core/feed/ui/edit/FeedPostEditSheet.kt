package com.us.android.core.feed.ui.edit

import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.FeedPostEditForm
import com.us.android.core.feed.data.FeedPostVisibility
import com.us.android.core.feed.data.PostEditRepository
import com.us.android.core.feed.data.feedPostEditPatch
import com.us.android.core.feed.data.feedPostEditProblem
import com.us.android.core.feed.data.feedPostEditRefusal
import com.us.android.core.feed.data.toFeedPostEditForm
import com.us.android.core.ui.UsSettingsOption
import com.us.android.core.ui.UsSettingsSelectRow
import com.us.android.core.ui.UsSettingsSwitchRow
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** The feed post's Edit sheet, as it draws. */
data class FeedPostEditUiState(
    val loading: Boolean = true,
    val loadError: String? = null,
    /** As the sheet opened: what Save diffs against. */
    val base: FeedPostEditForm = FeedPostEditForm(),
    val form: FeedPostEditForm = FeedPostEditForm(),
    /** The post carries a photo or a video: it may be left without text. */
    val hasMedia: Boolean = false,
    val saving: Boolean = false,
    val problem: String? = null,
    val saved: Boolean = false,
) {
    val dirty: Boolean get() = !feedPostEditPatch(base, form).isEmpty
}

/**
 * Behind [FeedPostEditSheet] (2026-10-24, the web's EditPostSheet): reads the
 * post's owner fields, holds the four the sheet edits, and saves ONLY what
 * moved with `PATCH /v1/posts/:id`. A landed save goes into `PostEdits`, which
 * the feeds lay over their rows, so the card shows the new words at once.
 */
@HiltViewModel
class FeedPostEditViewModel @Inject constructor(
    private val repository: PostEditRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(FeedPostEditUiState())
    val state: StateFlow<FeedPostEditUiState> = _state.asStateFlow()

    private var postId = ""

    /** The sheet opened on [id]; [hasMedia] is what the row on screen carries. */
    fun bind(id: String, hasMedia: Boolean) {
        if (id.isBlank()) return
        postId = id
        _state.value = FeedPostEditUiState(hasMedia = hasMedia)
        load()
    }

    fun retry() = load()

    private fun load() {
        val id = postId
        _state.update { it.copy(loading = true, loadError = null) }
        viewModelScope.launch {
            when (val result = repository.detail(id)) {
                is AppResult.Success -> {
                    val form = result.data.toFeedPostEditForm()
                    _state.update { it.copy(loading = false, base = form, form = form) }
                }
                is AppResult.Failure -> _state.update { it.copy(loading = false, loadError = feedPostEditRefusal(result.error)) }
            }
        }
    }

    fun change(transform: (FeedPostEditForm) -> FeedPostEditForm) =
        _state.update { it.copy(form = transform(it.form), problem = null) }

    fun save() {
        val now = _state.value
        if (now.saving || now.loading) return
        feedPostEditProblem(now.base, now.form, now.hasMedia)?.let { problem ->
            _state.update { it.copy(problem = problem) }
            return
        }
        val patch = feedPostEditPatch(now.base, now.form)
        if (patch.isEmpty) {
            _state.update { it.copy(saved = true) }
            return
        }
        _state.update { it.copy(saving = true, problem = null) }
        viewModelScope.launch {
            val result = repository.save(postId, patch)
            _state.update { current ->
                when (result) {
                    is AppResult.Success -> current.copy(saving = false, saved = true, base = current.form)
                    is AppResult.Failure -> current.copy(saving = false, problem = feedPostEditRefusal(result.error))
                }
            }
        }
    }
}

/**
 * "Edit post" — the owner's edit of their own published feed post, opened
 * from its More menu: the text, who can see it, whether comments are on, and
 * whether the like count shows. Members-only and the Action button are not
 * here, and a save never touches them.
 */
@Composable
fun FeedPostEditSheet(
    postId: String,
    hasMedia: Boolean,
    onDismiss: () -> Unit,
    viewModel: FeedPostEditViewModel = hiltViewModel(key = "feed-post-edit:$postId"),
) {
    LaunchedEffect(postId) { viewModel.bind(postId, hasMedia) }
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(state.saved) { if (state.saved) onDismiss() }

    OwnerSheet(
        title = "Edit post",
        subtitle = "Text, who can see it, comments, likes",
        icon = UsIcons.Pencil,
        testTag = "feed_post_edit_sheet",
        onDismiss = onDismiss,
    ) {
        when {
            state.loading -> Text(
                text = "Loading…",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textMuted,
            )
            state.loadError != null -> {
                SheetNote(text = state.loadError.orEmpty(), isError = true, testTag = "feed_post_edit_load_error")
                UsSecondaryButton(text = "Try again", onClick = viewModel::retry, modifier = Modifier.fillMaxWidth())
            }
            else -> {
                val form = state.form
                UsTextField(
                    value = form.text,
                    onValueChange = { value -> viewModel.change { it.copy(text = value) } },
                    label = "Text",
                    singleLine = false,
                    modifier = Modifier.fillMaxWidth().testTag("feed_post_edit_text"),
                )
                UsSettingsSelectRow(
                    title = "Who can see this",
                    selected = form.visibility,
                    options = FeedPostVisibility.choicesFor(state.base.visibility).map { UsSettingsOption(it.wire, it.label) },
                    onSelected = { value -> viewModel.change { it.copy(visibility = value) } },
                    enabled = !state.saving,
                )
                UsSettingsSwitchRow(
                    title = "Comments",
                    checked = form.allowComments,
                    onCheckedChange = { on -> viewModel.change { it.copy(allowComments = on) } },
                    enabled = !state.saving,
                )
                UsSettingsSwitchRow(
                    title = "Hide like count",
                    description = "Only you see how many people liked it.",
                    checked = form.hideLikeCount,
                    onCheckedChange = { on -> viewModel.change { it.copy(hideLikeCount = on) } },
                    enabled = !state.saving,
                )
                state.problem?.let { SheetNote(text = it, isError = true, testTag = "feed_post_edit_problem") }
                UsButton(
                    text = "Save",
                    onClick = viewModel::save,
                    enabled = state.dirty,
                    loading = state.saving,
                    modifier = Modifier.fillMaxWidth().testTag("feed_post_edit_save"),
                )
            }
        }
    }
}
