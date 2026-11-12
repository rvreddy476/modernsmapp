package com.us.android.feature.echo.ui

import androidx.compose.runtime.Immutable
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.common.result.AppResult
import com.us.android.core.model.SessionState
import com.us.android.feature.echo.data.EchoErrorCodes
import com.us.android.feature.echo.data.EchoMediaUploader
import com.us.android.feature.echo.data.EchoMediaUrls
import com.us.android.feature.echo.data.EchoRepository
import com.us.android.feature.echo.data.EchoRules
import com.us.android.feature.echo.data.EligibilityReason
import com.us.android.feature.echo.data.MEDIA_LIMIT_MESSAGE
import com.us.android.feature.echo.data.Note
import com.us.android.feature.echo.data.ReplyPolicy
import com.us.android.feature.echo.data.StagedMedia
import com.us.android.feature.echo.data.acceptedPicks
import com.us.android.feature.echo.data.canEditText
import com.us.android.feature.echo.data.echoCode
import com.us.android.feature.echo.data.echoErrorMessage
import com.us.android.feature.echo.data.eligibilityWords
import com.us.android.feature.echo.data.entryProblem
import com.us.android.feature.echo.di.EchoClock
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.time.Clock
import java.util.UUID
import javax.inject.Inject

/** One note of the draft. A thread is several. */
@Immutable
data class ComposerEntry(val key: Int, val text: String = "", val media: List<StagedMedia> = emptyList()) {
    val remaining: Int get() = EchoRules.remaining(text)
    val problem: String? get() = entryProblem(text, media)
}

@Immutable
data class ComposerUiState(
    val mode: ComposeMode = ComposeMode.NEW,
    val targetId: String = "",
    val entries: List<ComposerEntry> = listOf(ComposerEntry(key = 0)),
    val replyPolicy: ReplyPolicy = ReplyPolicy.EVERYONE,
    /** The note being replied to, quoted or edited, once read. */
    val target: Note? = null,
    /** Eligibility (or, for an edit, the note) is being read; nothing can be sent yet. */
    val checking: Boolean = true,
    /** The viewer may not post this: the sheet replaces the composer. */
    val blocked: ComposeGate.Blocked? = null,
    /** Edit only: the 30-minute window is over, so only who can reply may change. */
    val textLocked: Boolean = false,
    val submitting: Boolean = false,
    val error: String? = null,
    /** A short notice that is not a failure: picks over the limit were left out. */
    val notice: String? = null,
    /** Posted: the id of the note to open (the top of a thread). */
    val doneNoteId: String? = null,
) {
    val isThread: Boolean get() = entries.size > 1

    /** Threads are for new notes only; a reply, a quote and an edit are one note. */
    val canAddEntry: Boolean get() = mode == ComposeMode.NEW && entries.size < EchoRules.THREAD_MAX && !submitting

    /** Media cannot change on an edit (PATCH takes text and reply policy only). */
    val canAddMedia: Boolean get() = mode != ComposeMode.EDIT && !submitting

    /** Replies take their target's conversation; everything else picks who can reply. */
    val showsReplyPolicy: Boolean get() = mode != ComposeMode.REPLY

    /** The first thing stopping Post, or null. */
    val problem: String? get() = when {
        mode == ComposeMode.EDIT -> entries.first().problem.takeUnless { textLocked }
        entries.size > EchoRules.THREAD_MAX -> "A thread can have up to ${EchoRules.THREAD_MAX} notes."
        else -> entries.firstNotNullOfOrNull { it.problem }
    }

    val canSubmit: Boolean get() = !checking && blocked == null && !submitting && problem == null && doneNoteId == null
}

/**
 * The Echo composer: a note, a thread (2–25), a reply, a quote, or an edit.
 *
 *  - It reads eligibility before anything can be sent (new note, thread and
 *    quote need `can_post`; a reply `can_reply` and the note's own
 *    `viewer_can_reply`). The screens that open it ask first too; this is
 *    the backstop for a composer reached any other way.
 *  - 500 characters a note, up to 4 photos or 1 GIF, through the same upload
 *    pipeline the post composer uses.
 *  - One `Idempotency-Key` per submitted draft: a retry of the same draft
 *    sends the same key; any change to the draft mints a new one.
 */
@HiltViewModel
class ComposerViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val repository: EchoRepository,
    private val uploader: EchoMediaUploader,
    private val media: EchoMediaUrls,
    private val session: SessionStateProvider,
    @EchoClock private val clock: Clock,
) : ViewModel() {

    private val _state = MutableStateFlow(
        ComposerUiState(
            mode = ComposeMode.fromWire(savedStateHandle.get<String>(MODE_KEY)),
            targetId = savedStateHandle.get<String>(TARGET_KEY).orEmpty(),
        ),
    )
    val state: StateFlow<ComposerUiState> = _state.asStateFlow()
    val mediaUrls: StateFlow<Map<String, String>> = media.urls

    private var nextKey = 1

    /** The key of the draft last sent; null once the draft changes or posts. */
    private var idempotencyKey: String? = null

    private val viewerId: String
        get() = (session.sessionState.value as? SessionState.Authenticated)?.userId.orEmpty()

    init {
        viewModelScope.launch { prepare() }
    }

    private suspend fun prepare() {
        val s = _state.value
        val target = if (s.targetId.isNotBlank()) {
            when (val read = repository.note(s.targetId)) {
                is AppResult.Success -> read.data.also { media.load(listOf(it)) }
                is AppResult.Failure -> {
                    // Nothing to reply to, quote or edit: nothing can be sent.
                    _state.update {
                        it.copy(
                            checking = false,
                            blocked = ComposeGate.Blocked(NOTE_UNAVAILABLE_TITLE, echoErrorMessage(read.error))
                        )
                    }
                    return
                }
            }
        } else {
            null
        }
        if (s.mode == ComposeMode.EDIT) {
            val note = target ?: return _state.update { it.copy(checking = false) }
            _state.update {
                it.copy(
                    target = note,
                    checking = false,
                    entries = listOf(ComposerEntry(key = 0, text = note.text)),
                    replyPolicy = note.replyPolicy,
                    textLocked = !canEditText(note, viewerId, clock.instant()),
                )
            }
            return
        }
        val gate = readGate(repository) { eligibility ->
            if (s.mode == ComposeMode.REPLY && target != null) replyGate(eligibility, target) else postGate(eligibility)
        }
        _state.update {
            when (gate) {
                ComposeGate.Open -> it.copy(target = target, checking = false)
                is ComposeGate.Blocked -> it.copy(target = target, checking = false, blocked = gate)
                // Fail closed: nothing is sent without a good answer.
                is ComposeGate.Failed -> it.copy(
                    target = target,
                    checking = false,
                    blocked = ComposeGate.Blocked(CANNOT_CHECK_TITLE, gate.message)
                )
            }
        }
    }

    fun setText(index: Int, text: String) {
        if (_state.value.textLocked) return
        draftChanged { s ->
            s.copy(
                entries = s.entries.mapIndexed { i, e -> if (i == index) e.copy(text = text) else e }
            )
        }
    }

    fun setReplyPolicy(policy: ReplyPolicy) = draftChanged { it.copy(replyPolicy = policy) }

    /** Thread mode: one more note under the last, up to 25. */
    fun addEntry() {
        if (!_state.value.canAddEntry) return
        draftChanged { it.copy(entries = it.entries + ComposerEntry(key = nextKey++)) }
    }

    fun removeEntry(index: Int) {
        if (_state.value.entries.size <= 1) return
        draftChanged { s -> s.copy(entries = s.entries.filterIndexed { i, _ -> i != index }) }
    }

    /**
     * Photos picked for entry [index]. Those that fit the rule (4 photos, or
     * 1 GIF alone) are staged and uploaded; the rest are left out, and the
     * composer says so.
     */
    fun addMedia(index: Int, uris: List<String>) {
        val s = _state.value
        if (!s.canAddMedia || uris.isEmpty()) return
        val entry = s.entries.getOrNull(index) ?: return
        val fresh = uris.filter { uri -> entry.media.none { it.localKey == uri } }
        val gifs = fresh.map { uploader.isGif(it) }
        val accepted = acceptedPicks(entry.media, gifs)
        val staged = fresh.take(accepted).mapIndexed { i, uri -> StagedMedia(localKey = uri, isGif = gifs[i]) }
        draftChanged { st ->
            st.copy(
                entries = st.entries.mapIndexed { i, e -> if (i == index) e.copy(media = e.media + staged) else e },
                notice = if (accepted < fresh.size) MEDIA_LIMIT_MESSAGE else null,
            )
        }
        staged.forEach { upload(it.localKey) }
    }

    private fun upload(localKey: String) {
        viewModelScope.launch {
            val result = uploader.upload(localKey)
            _state.update { s ->
                s.copy(
                    entries = s.entries.map { e ->
                        e.copy(
                            media = e.media.map { m ->
                                if (m.localKey != localKey) {
                                    m
                                } else {
                                    when (result) {
                                        is AppResult.Success -> m.copy(mediaId = result.data, failed = false)
                                        is AppResult.Failure -> m.copy(failed = true)
                                    }
                                }
                            },
                        )
                    },
                )
            }
            idempotencyKey = null
        }
    }

    fun removeMedia(index: Int, localKey: String) = draftChanged { s ->
        s.copy(
            entries = s.entries.mapIndexed { i, e ->
                if (i == index) {
                    e.copy(
                        media = e.media.filterNot { it.localKey == localKey }
                    )
                } else {
                    e
                }
            }
        )
    }

    fun noticeShown() = _state.update { it.copy(notice = null) }

    fun errorShown() = _state.update { it.copy(error = null) }

    @Suppress("CyclomaticComplexMethod") // new, reply, quote, thread and edit share one key and one guard
    fun submit() {
        val s = _state.value
        if (!s.canSubmit) return
        val key = idempotencyKey ?: UUID.randomUUID().toString().also { idempotencyKey = it }
        _state.update { it.copy(submitting = true, error = null) }
        viewModelScope.launch {
            val result: AppResult<String> = when {
                s.mode == ComposeMode.EDIT -> edit(s)
                s.entries.size >= EchoRules.THREAD_MIN -> thread(s, key)
                else -> single(s, key)
            }
            when (result) {
                is AppResult.Success -> {
                    idempotencyKey = null
                    _state.update { it.copy(submitting = false, doneNoteId = result.data) }
                }
                is AppResult.Failure -> {
                    val code = echoCode(result.error)
                    val reason = when (code) {
                        EchoErrorCodes.PREMIUM_REQUIRED -> EligibilityReason.PREMIUM_REQUIRED
                        EchoErrorCodes.ACCOUNT_TOO_NEW -> EligibilityReason.ACCOUNT_TOO_NEW
                        else -> null
                    }
                    val sheet = reason?.let(
                        ::eligibilityWords
                    )?.let { (title, detail) -> ComposeGate.Blocked(title, detail) }
                    _state.update {
                        it.copy(
                            submitting = false,
                            blocked = sheet ?: it.blocked,
                            error = if (sheet == null) echoErrorMessage(result.error) else null
                        )
                    }
                }
            }
        }
    }

    private suspend fun single(s: ComposerUiState, key: String): AppResult<String> {
        val entry = s.entries.first()
        return repository.post(
            idempotencyKey = key,
            text = entry.text,
            mediaIds = entry.media.mapNotNull { it.mediaId },
            replyToId = s.targetId.takeIf { s.mode == ComposeMode.REPLY },
            quoteOfId = s.targetId.takeIf { s.mode == ComposeMode.QUOTE },
            replyPolicy = s.replyPolicy.takeIf { s.showsReplyPolicy },
        ).idOf()
    }

    private suspend fun thread(s: ComposerUiState, key: String): AppResult<String> =
        when (
            val result = repository.postThread(
                idempotencyKey = key,
                entries = s.entries.map { e -> e.text to e.media.mapNotNull { it.mediaId } },
                replyPolicy = s.replyPolicy,
            )
        ) {
            is AppResult.Success -> AppResult.Success(result.data.firstOrNull()?.id.orEmpty())
            is AppResult.Failure -> result
        }

    private suspend fun edit(s: ComposerUiState): AppResult<String> {
        val original = s.target ?: return AppResult.Success(s.targetId)
        val text = s.entries.first().text.takeIf { !s.textLocked && it.trim() != original.text.trim() }
        val policy = s.replyPolicy.takeIf { it != original.replyPolicy }
        if (text == null && policy == null) return AppResult.Success(original.id)
        return repository.edit(original.id, text, policy).idOf()
    }

    private fun AppResult<Note>.idOf(): AppResult<String> = when (this) {
        is AppResult.Success -> AppResult.Success(data.id)
        is AppResult.Failure -> this
    }

    /** Any change to the draft makes it a new submission, with a new key. */
    private fun draftChanged(transform: (ComposerUiState) -> ComposerUiState) {
        if (_state.value.submitting) return
        idempotencyKey = null
        _state.update { transform(it).copy(error = null) }
    }

    /** The key the next submit would send; for tests. */
    internal fun pendingIdempotencyKey(): String? = idempotencyKey

    companion object {
        const val MODE_KEY = "mode"
        const val TARGET_KEY = "targetId"
        const val CANNOT_CHECK_TITLE = "We couldn't check if you can post"
        const val NOTE_UNAVAILABLE_TITLE = "This note isn't available"
    }
}
