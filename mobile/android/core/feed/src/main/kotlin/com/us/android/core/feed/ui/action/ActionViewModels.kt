package com.us.android.core.feed.ui.action

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.datastore.LeavingDomainsStore
import com.us.android.core.feed.data.action.ActionLinkResult
import com.us.android.core.feed.data.action.ActionRepository
import com.us.android.core.model.ActionButtons
import com.us.android.core.model.ActionLabel
import com.us.android.core.model.ActionLinks
import com.us.android.core.model.ActionStats
import com.us.android.core.model.PostAction
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** Where a tap on an Action button stands. [owner] is the button that asked, so only it draws the page. */
sealed interface ActionLinkState {
    val owner: String

    data object Idle : ActionLinkState {
        override val owner: String = ""
    }

    /** `GET /v1/links/:id` is on its way. */
    data class Loading(override val owner: String, val linkId: String) : ActionLinkState

    /** A domain this person has not continued to before: "You're leaving VChat". */
    data class Leaving(override val owner: String, val target: ActionLinks.Target) : ActionLinkState

    /** 404, or nothing we may open. */
    data class Gone(override val owner: String) : ActionLinkState

    data class Failed(override val owner: String, val linkId: String, val message: String) : ActionLinkState
}

/**
 * Behind every Action button's tap (2026-10-23). Never opens the raw url of
 * a row: it asks `GET /v1/links/:id` for the target (which counts the click),
 * then either opens it at once — a domain continued to before — or shows the
 * leaving page first. Continue remembers the domain ([LeavingDomainsStore],
 * once per site per person), then opens. [launch] is the url the screen must
 * hand to the browser, consumed with [launched].
 */
@HiltViewModel
class ActionLinkViewModel @Inject constructor(
    private val repository: ActionRepository,
    private val seenDomains: LeavingDomainsStore,
) : ViewModel() {

    private val _state = MutableStateFlow<ActionLinkState>(ActionLinkState.Idle)
    val state: StateFlow<ActionLinkState> = _state.asStateFlow()

    /** A url to open now, and the button it is for; null when there is none. */
    private val _launch = MutableStateFlow<Pair<String, String>?>(null)
    val launch: StateFlow<Pair<String, String>?> = _launch.asStateFlow()

    private var inFlight: Job? = null

    /** A tap on [action], from the button [owner]. Only a live button is ever opened. */
    fun open(owner: String, action: PostAction) {
        if (ActionButtons.visible(action, isAuthor = false) == null) return
        resolve(owner, action.id)
    }

    fun retry() {
        val failed = _state.value as? ActionLinkState.Failed ?: return
        resolve(failed.owner, failed.linkId)
    }

    /** Continue on the leaving page: remember the domain, then go. */
    fun continueToSite() {
        val leaving = _state.value as? ActionLinkState.Leaving ?: return
        _state.value = ActionLinkState.Idle
        viewModelScope.launch {
            runCatching { seenDomains.remember(leaving.target.domain) }
            _launch.value = leaving.owner to leaving.target.url
        }
    }

    /** Go back, or the page was dismissed: nothing opens. */
    fun dismiss() {
        inFlight?.cancel()
        _state.value = ActionLinkState.Idle
    }

    /** The screen handed [launch] to the browser. */
    fun launched() {
        _launch.value = null
    }

    private fun resolve(owner: String, linkId: String) {
        inFlight?.cancel()
        _state.value = ActionLinkState.Loading(owner, linkId)
        inFlight = viewModelScope.launch {
            val next = when (val result = repository.link(linkId)) {
                is ActionLinkResult.Ready -> {
                    val seen = runCatching { seenDomains.seen() }.getOrDefault(emptyList())
                    when (val visit = ActionLinks.decide(result.target, seen)) {
                        is ActionLinks.Visit.Open -> {
                            _launch.value = owner to visit.url
                            ActionLinkState.Idle
                        }
                        is ActionLinks.Visit.Interstitial -> ActionLinkState.Leaving(owner, result.target)
                    }
                }
                ActionLinkResult.Gone -> ActionLinkState.Gone(owner)
                is ActionLinkResult.Failed -> ActionLinkState.Failed(owner, linkId, result.message)
            }
            _state.update { current -> if (current is ActionLinkState.Loading && current.owner == owner) next else current }
        }
    }
}

/** The label picker's options: the server's list once read, the contract's until then (and whenever it cannot be). */
@HiltViewModel
class ActionLabelsViewModel @Inject constructor(
    private val repository: ActionRepository,
) : ViewModel() {
    private val _labels = MutableStateFlow(ActionButtons.LABELS)
    val labels: StateFlow<List<ActionLabel>> = _labels.asStateFlow()

    init {
        viewModelScope.launch { _labels.value = repository.labels() }
    }
}

/**
 * The creator's taps per item (`GET /v1/posts/:id/action/stats`), one read
 * per post per screen. Keyed by post id so one instance serves a whole list.
 * A post whose read failed stays absent: the row says nothing rather than "0 taps".
 */
@HiltViewModel
class ActionStatsViewModel @Inject constructor(
    private val repository: ActionRepository,
) : ViewModel() {
    private val _stats = MutableStateFlow<Map<String, ActionStats>>(emptyMap())
    val stats: StateFlow<Map<String, ActionStats>> = _stats.asStateFlow()

    private val asked = HashSet<String>()

    fun ensure(postId: String) {
        if (postId.isBlank() || !asked.add(postId)) return
        viewModelScope.launch {
            val read = repository.stats(postId) ?: return@launch
            _stats.update { it + (postId to read) }
        }
    }
}
