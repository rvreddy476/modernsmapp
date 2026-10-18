package com.us.android.feature.post.drafts

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.media.data.MediaRepository
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.post.createhub.ReelLookups
import com.us.android.feature.post.createhub.TaggedUser
import com.us.android.feature.post.data.DraftContent
import com.us.android.feature.post.data.PostDraft
import com.us.android.feature.post.data.PostDraftsRepository
import com.us.android.feature.post.studio.DetailsTile
import com.us.android.feature.post.studio.PostDetailsForm
import com.us.android.feature.post.studio.PostDetailsFormActions
import com.us.android.feature.post.studio.PostDetailsFormState
import com.us.android.feature.post.studio.PostDetailsUi
import com.us.android.feature.post.studio.RecentLocations
import com.us.android.feature.post.studio.insertHashMark
import com.us.android.feature.post.studio.insertMention
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** A draft open in the editor. */
data class DraftEditorUiState(
    val loading: Boolean = true,
    val failed: Boolean = false,
    val draft: PostDraft? = null,
    val text: String = "",
    val details: PostDetailsUi = PostDetailsUi(),
    val altTexts: Map<String, String> = emptyMap(),
    val thumbs: Map<String, String?> = emptyMap(),
    val recentLocations: List<String> = emptyList(),
    val mentionQuery: String = "",
    val mentionResults: List<TaggedUser> = emptyList(),
    val saving: Boolean = false,
    val publishing: Boolean = false,
    val notice: String? = null,
    /** Saved: back to the list. */
    val saved: Boolean = false,
    /** Published: the post it became. */
    val publishedPostId: String? = null,
)

/**
 * A saved draft, open again in the details form (2026-10-18): its photos are
 * already on the server, so the strip shows them and picks the cover among
 * them, and everything else is edited as on the studio's details step. Save
 * changes the draft; Post saves it and publishes it.
 */
@HiltViewModel
class DraftEditorViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val drafts: PostDraftsRepository,
    private val media: MediaRepository,
    private val lookups: ReelLookups,
    private val recentLocations: RecentLocations,
) : ViewModel() {
    private val draftId: String = savedStateHandle.get<String>("draftId").orEmpty()
    private val _state = MutableStateFlow(DraftEditorUiState())
    val state: StateFlow<DraftEditorUiState> = _state.asStateFlow()
    private var mentionSearch: Job? = null

    init {
        load()
        viewModelScope.launch {
            val recent = recentLocations.load()
            _state.update { it.copy(recentLocations = recent) }
        }
    }

    fun load() {
        _state.update { it.copy(loading = true, failed = false) }
        viewModelScope.launch {
            when (val result = drafts.get(draftId)) {
                is AppResult.Success -> {
                    val draft = result.data
                    _state.update {
                        it.copy(
                            loading = false,
                            draft = draft,
                            text = draft.text,
                            details = PostDetailsUi.from(draft.details).withPageCount(draft.mediaIds.size),
                            altTexts = draft.altTexts,
                        )
                    }
                    draft.mediaIds.forEach { mediaId ->
                        launch {
                            val url = (media.delivery(mediaId) as? AppResult.Success)?.data?.posterUrl
                            _state.update { it.copy(thumbs = it.thumbs + (mediaId to url)) }
                        }
                    }
                }
                is AppResult.Failure -> _state.update { it.copy(loading = false, failed = true) }
            }
        }
    }

    fun onTextChanged(value: String) = _state.update { it.copy(text = value) }

    fun onDetails(change: (PostDetailsUi) -> PostDetailsUi) = _state.update { state ->
        state.copy(details = change(state.details).withPageCount(state.draft?.mediaIds?.size ?: 0))
    }

    fun onInsertHashtag() = onTextChanged(insertHashMark(_state.value.text))

    fun onMentionPicked(user: TaggedUser) =
        _state.update { it.copy(text = insertMention(it.text, user.username), mentionQuery = "", mentionResults = emptyList()) }

    fun onMentionQueryChanged(query: String) {
        _state.update { it.copy(mentionQuery = query) }
        mentionSearch?.cancel()
        val trimmed = query.trim().removePrefix("@")
        if (trimmed.isEmpty()) {
            _state.update { it.copy(mentionResults = emptyList()) }
            return
        }
        mentionSearch = viewModelScope.launch {
            delay(MENTION_DEBOUNCE_MILLIS)
            val found = lookups.searchPeople(trimmed)
            _state.update { it.copy(mentionResults = found) }
        }
    }

    fun onLocationChosen(name: String) {
        onDetails { it.copy(locationName = name.trim(), locationPlaceId = null) }
        if (name.isNotBlank()) {
            viewModelScope.launch {
                val recent = recentLocations.remember(name)
                _state.update { it.copy(recentLocations = recent) }
            }
        }
    }

    fun onAltText(index: Int, altText: String) {
        val mediaId = _state.value.draft?.mediaIds?.getOrNull(index) ?: return
        _state.update { it.copy(altTexts = it.altTexts + (mediaId to altText)) }
    }

    /** Saves the changes; [then] runs only when the save landed. */
    fun onSave(then: (suspend () -> Unit)? = null) {
        val current = _state.value
        val draft = current.draft ?: return
        if (current.saving || current.publishing || !current.details.valid) return
        _state.update { it.copy(saving = then == null, publishing = then != null) }
        viewModelScope.launch {
            val content = DraftContent(
                text = current.text,
                language = draft.language,
                mediaIds = draft.mediaIds,
                altTexts = current.altTexts,
                details = current.details.toDetails(current.text, draft.mediaIds.size),
            )
            when (drafts.update(draft.id, content)) {
                is AppResult.Success -> if (then == null) {
                    _state.update { it.copy(saving = false, saved = true) }
                } else {
                    then()
                }
                is AppResult.Failure ->
                    _state.update { it.copy(saving = false, publishing = false, notice = "The draft couldn't be saved. Try again.") }
            }
        }
    }

    /** Saves what is on screen, then publishes the draft now. */
    fun onPublish() = onSave {
        when (val published = drafts.publish(draftId)) {
            is AppResult.Success -> _state.update { it.copy(publishing = false, publishedPostId = published.data) }
            is AppResult.Failure -> _state.update { it.copy(publishing = false, notice = "This draft couldn't be posted. Try again.") }
        }
    }

    fun onNoticeShown() = _state.update { it.copy(notice = null) }

    private companion object {
        const val MENTION_DEBOUNCE_MILLIS = 250L
    }
}

@Composable
fun DraftEditorScreen(
    onBack: () -> Unit,
    onSaved: () -> Unit,
    onPublished: (postId: String) -> Unit,
    viewModel: DraftEditorViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(state.saved) { if (state.saved) onSaved() }
    LaunchedEffect(state.publishedPostId) { state.publishedPostId?.let(onPublished) }
    val draft = state.draft
    when {
        state.loading -> UsLoadingState(label = "Loading draft")
        state.failed || draft == null -> UsErrorState(message = "We couldn't open this draft.", onRetry = viewModel::load)
        else -> Box(modifier = Modifier.fillMaxSize()) {
            PostDetailsForm(
                state = PostDetailsFormState(
                    tiles = draft.mediaIds.map { id -> DetailsTile(key = id, model = state.thumbs[id], altText = state.altTexts[id].orEmpty()) },
                    description = state.text,
                    details = state.details,
                    recentLocations = state.recentLocations,
                    mentionQuery = state.mentionQuery,
                    mentionResults = state.mentionResults,
                    posting = state.publishing,
                    savingDraft = state.saving,
                    canPost = state.details.valid && (draft.mediaIds.isNotEmpty() || state.text.isNotBlank()),
                    draftsLabel = "Save",
                ),
                actions = PostDetailsFormActions(
                    onBack = onBack,
                    onPreview = null,
                    onDescriptionChanged = viewModel::onTextChanged,
                    onInsertHashtag = viewModel::onInsertHashtag,
                    onMentionQueryChanged = viewModel::onMentionQueryChanged,
                    onMentionPicked = viewModel::onMentionPicked,
                    onDetails = viewModel::onDetails,
                    onLocationChosen = viewModel::onLocationChosen,
                    onAltText = { index, alt, _ -> viewModel.onAltText(index, alt) },
                    // The photos are on the server already; adding more is the studio's job.
                    onAddPhotos = null,
                    onDrafts = { viewModel.onSave() },
                    onPost = viewModel::onPublish,
                ),
            )
            state.notice?.let { message ->
                Text(
                    text = message,
                    color = UsTheme.extended.statusDanger,
                    style = MaterialTheme.typography.bodySmall,
                    modifier = Modifier.align(Alignment.TopCenter).padding(UsTheme.spacing.xxxxl),
                )
                LaunchedEffect(message) {
                    delay(NOTICE_MILLIS)
                    viewModel.onNoticeShown()
                }
            }
        }
    }
}

private const val NOTICE_MILLIS = 3_000L
