package com.us.android.core.feed.ui.edit

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.engagement.data.CommentMentionSource
import com.us.android.core.feed.data.FeedCategory
import com.us.android.core.feed.data.PostEditForm
import com.us.android.core.feed.data.PostEditPatch
import com.us.android.core.feed.data.PostEditRepository
import com.us.android.core.feed.data.VideoFeedRepository
import com.us.android.core.feed.data.credits.CreditPerson
import com.us.android.core.feed.data.credits.CreditsForm
import com.us.android.core.feed.data.credits.CreditsRepository
import com.us.android.core.feed.data.credits.creditRefusal
import com.us.android.core.feed.data.credits.creditsOffered
import com.us.android.core.feed.data.creditsForm
import com.us.android.core.feed.data.creditsPatch
import com.us.android.core.feed.data.postEditPatch
import com.us.android.core.feed.data.postEditProblems
import com.us.android.core.feed.data.postEditRefusal
import com.us.android.core.feed.data.serverCode
import com.us.android.core.feed.data.toEditForm
import com.us.android.core.feed.ui.credits.CreditsEditor
import com.us.android.core.memberships.data.MembershipLevel
import com.us.android.core.memberships.data.MembershipsRepository
import com.us.android.core.memberships.data.membersOnlyRefusal
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
    /** The credits as the sheet opened (video credits, 2026-10-23); the editor holds them as they are now. */
    val creditsBase: CreditsForm = CreditsForm(),
    val saving: Boolean = false,
    /** The checks before Save, or the server's refusal. */
    val problems: List<String> = emptyList(),
    /** The save landed; the sheet leaves. */
    val saved: Boolean = false,
    /**
     * Members only (paid memberships, 2026-10-23): the owner's ACTIVE levels
     * (the picker is drawn only when there is one), the level the post was
     * gated on as the sheet opened, and the one picked now (null = everyone).
     */
    val levels: List<MembershipLevel> = emptyList(),
    val gateBase: String? = null,
    val gate: String? = null,
) {
    /** Something moved: Save has a patch to send, or a members-only change to apply. */
    val dirty: Boolean get() = !postEditPatch(base, form).isEmpty || gateDirty

    /** The members-only level moved. */
    val gateDirty: Boolean get() = gate != gateBase

    /**
     * The picker is offered: the owner has an active level, or the post is
     * already gated (on a level since turned off), so it can still be opened.
     */
    val offersMembersOnly: Boolean get() = levels.isNotEmpty() || gateBase != null

    /**
     * The Credits section is shown: a long video in a category that asks
     * for credits — or one that already has some, so they can still be
     * changed or cleared after its category moved.
     */
    val offersCredits: Boolean get() = longVideo && (creditsOffered(form.category) || !creditsBase.isEmpty)

    /** Whether the credits as [now] differ from how the sheet opened. */
    fun creditsDirty(now: CreditsForm): Boolean = offersCredits && !creditsPatch(creditsBase, now).isEmpty
}

/**
 * Behind [PostEditSheet] (2026-10-21, the web's Edit sheet): reads the
 * post's owner fields, holds the form, and saves ONLY what changed with
 * `PATCH /v1/posts/:id`. A landed save is recorded in `PostEdits`, which
 * every list layers over its rows, so the reel or the video on screen shows
 * the new words at once.
 *
 * A long video's credits (2026-10-23) are edited in the same sheet through
 * [credits], each linked credit showing where its tag stands; they ride the
 * same PATCH, and the list is sent WHOLE when anything in it moved (the
 * server replaces it).
 */
@HiltViewModel
class PostEditViewModel @Inject constructor(
    private val repository: PostEditRepository,
    private val videos: VideoFeedRepository,
    creditsRepository: CreditsRepository,
    mentions: CommentMentionSource,
    private val memberships: MembershipsRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(PostEditUiState())
    val state: StateFlow<PostEditUiState> = _state.asStateFlow()

    /** The Credits section's form, search and genres. */
    val credits = CreditsEditor(
        scope = viewModelScope,
        searchPeople = { query ->
            mentions.search(query).map {
                CreditPerson(id = it.userId, name = it.displayName.ifBlank { it.username }, username = it.username)
            }
        },
        loadGenres = creditsRepository::genres,
    )

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
        viewModelScope.launch {
            val levels = memberships.ownActiveLevels()
            _state.update { it.copy(levels = levels) }
        }
        if (longVideo) credits.ensureGenres()
    }

    fun retry() = load()

    private fun load() {
        val id = postId
        _state.update { it.copy(loading = true, loadError = null) }
        viewModelScope.launch {
            when (val result = repository.detail(id)) {
                is AppResult.Success -> {
                    val form = result.data.toEditForm()
                    val creditsForm = result.data.creditsForm { credits.newKey() }
                    credits.reset(creditsForm)
                    val gate = result.data.tierRequiredId?.takeIf { it.isNotBlank() }
                    _state.update {
                        it.copy(
                            loading = false,
                            base = form,
                            form = form,
                            creditsBase = creditsForm,
                            gateBase = gate,
                            gate = gate,
                        )
                    }
                }
                is AppResult.Failure ->
                    _state.update { it.copy(loading = false, loadError = postEditRefusal(result.error)) }
            }
        }
    }

    /** Members only: a level, or null for everyone. Applied on Save. */
    fun selectGate(levelId: String?) = _state.update { it.copy(gate = levelId, problems = emptyList()) }

    /** Every field's change goes through here: the sheet hands back the form with one field moved. */
    fun change(transform: (PostEditForm) -> PostEditForm) =
        _state.update { it.copy(form = transform(it.form), problems = emptyList()) }

    fun save() {
        val now = _state.value
        if (now.saving || now.loading) return
        val problems = postEditProblems(now.form)
        val creditProblems = if (now.offersCredits) credits.validate() else emptyList()
        if (problems.isNotEmpty() || creditProblems.isNotEmpty()) {
            _state.update { it.copy(problems = problems) }
            return
        }
        val creditsNow = credits.state.value.form
        val patch = postEditPatch(now.base, now.form)
            .then(if (now.offersCredits) creditsPatch(now.creditsBase, creditsNow) else PostEditPatch())
            .then(gatePatch(now))
        if (patch.isEmpty) {
            _state.update { it.copy(saved = true) }
            return
        }
        _state.update { it.copy(saving = true, problems = emptyList()) }
        viewModelScope.launch {
            _state.update { current ->
                when (val result = repository.save(postId, patch)) {
                    is AppResult.Success -> current.copy(
                        saving = false,
                        saved = true,
                        base = current.form,
                        creditsBase = creditsNow,
                        gateBase = current.gate,
                    )
                    is AppResult.Failure -> {
                        // A refusal of the credits fields is said under the Credits section, beside what it is about.
                        val credited = creditRefusal(result.error.serverCode())
                        if (credited != null) credits.showRefusal(credited)
                        val problem = membersOnlyRefusal(result.error.serverCode()) ?: postEditRefusal(result.error)
                        current.copy(
                            saving = false,
                            problems = if (credited != null) emptyList() else listOf(problem),
                        )
                    }
                }
            }
        }
    }
}

/**
 * The members-only change, in the same PATCH (post-service post_edit.go:97,
 * 2026-10-23): a level sets the gate, `""` clears it (the platform `Json`
 * drops a Kotlin null, which means "leave it").
 */
internal fun gatePatch(state: PostEditUiState): PostEditPatch =
    if (state.gateDirty) PostEditPatch(tierRequiredId = state.gate ?: "") else PostEditPatch()
