package com.us.android.feature.chat.ui

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.chat.data.ChatSessionManager
import com.us.android.core.chat.data.ChatSocketEvent
import com.us.android.core.chat.data.Conversation
import com.us.android.core.chat.data.MessageConsentRepository
import com.us.android.core.chat.data.RequestPageKey
import com.us.android.core.chat.data.isMissingRoute
import com.us.android.core.chat.data.requestCountLabel
import com.us.android.core.chat.data.requestTitle
import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.SessionState
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * The requests folder (DM consent, 2026-10-20): the waiting requests a page
 * at a time, and "Hidden requests" — the ones safe requests set aside — as
 * an entry at the foot of the list rather than a tab, so the risky ones are
 * one deliberate tap away and never mixed in.
 */
data class RequestsFolderState(
    /** True while the Hidden requests sub-list is on screen. */
    val hidden: Boolean = false,
    val rows: List<Conversation> = emptyList(),
    val loading: Boolean = true,
    val loadingMore: Boolean = false,
    val failed: Boolean = false,
    /** Where the next page starts; null = everything is on screen. */
    val next: RequestPageKey? = null,
    /** Hidden requests waiting, from the server's count; null = unknown (the entry still shows). */
    val hiddenCount: Int? = null,
    /** The server stopped counting: the entry reads "999+". */
    val hiddenCapped: Boolean = false,
    /** False when this server has no Hidden requests list: the entry hides. */
    val hiddenAvailable: Boolean = true,
    val viewerId: String = "",
) {
    val canLoadMore: Boolean get() = next != null && !loading && !loadingMore && !failed
}

@HiltViewModel
class MessageRequestsViewModel @Inject constructor(
    private val consent: MessageConsentRepository,
    session: SessionStateProvider,
    // The realtime frames (`message_request_removed`); null in tests.
    chatSession: ChatSessionManager? = null,
) : ViewModel() {

    private val _state = MutableStateFlow(
        RequestsFolderState(viewerId = (session.sessionState.value as? SessionState.Authenticated)?.userId.orEmpty()),
    )
    val state: StateFlow<RequestsFolderState> = _state.asStateFlow()

    private var pageJob: Job? = null

    init {
        refresh()
        chatSession?.let { live ->
            viewModelScope.launch {
                live.events.collect { event ->
                    if (event is ChatSocketEvent.MessageRequestRemoved) onRequestRemoved(event.conversationId)
                }
            }
        }
    }

    /** A request left Requests (it expired): its row goes at once, and the counts are read again. */
    fun onRequestRemoved(conversationId: String) {
        _state.update { now -> now.copy(rows = now.rows.filterNot { it.id == conversationId }) }
        viewModelScope.launch { readCounts() }
    }

    /** The current list from its first page; also re-reads the hidden count. */
    fun refresh() {
        val hidden = _state.value.hidden
        pageJob?.cancel()
        _state.update { it.copy(loading = true, failed = false) }
        pageJob = viewModelScope.launch {
            when (val page = consent.requestsPage(hidden = hidden)) {
                is AppResult.Success -> _state.update {
                    it.copy(loading = false, rows = page.data.items, next = page.data.next)
                }
                is AppResult.Failure -> _state.update {
                    if (hidden && page.error.isMissingRoute()) {
                        it.copy(loading = false, hidden = false, hiddenAvailable = false)
                    } else {
                        it.copy(loading = false, failed = true)
                    }
                }
            }
            readCounts()
        }
    }

    /** The next page, appended; rows already on screen are not repeated. */
    fun loadMore() {
        val current = _state.value
        val key = current.next ?: return
        if (!current.canLoadMore) return
        _state.update { it.copy(loadingMore = true) }
        pageJob = viewModelScope.launch {
            when (val page = consent.requestsPage(hidden = current.hidden, key = key)) {
                is AppResult.Success -> _state.update { now ->
                    val seen = now.rows.map { it.id }.toSet()
                    now.copy(
                        loadingMore = false,
                        rows = now.rows + page.data.items.filter { it.id !in seen },
                        next = page.data.next,
                    )
                }
                // The rows on screen stay; the next scroll to the foot tries again.
                is AppResult.Failure -> _state.update { it.copy(loadingMore = false) }
            }
        }
    }

    fun showHidden() = switchTo(hidden = true)

    fun showAll() = switchTo(hidden = false)

    private fun switchTo(hidden: Boolean) {
        if (_state.value.hidden == hidden) return
        _state.update { it.copy(hidden = hidden, rows = emptyList(), next = null) }
        refresh()
    }

    /** `GET /v1/chat/requests/count`: the Hidden requests entry's number. */
    private suspend fun readCounts() {
        val counts = (consent.requestCounts() as? AppResult.Success)?.data ?: return
        _state.update { it.copy(hiddenCount = counts.hidden, hiddenCapped = counts.capped) }
    }
}

/** Message requests — the Chats tab's door. Tapping one opens its decision screen (Accept, Delete, Block, Report). */
@Composable
fun RequestsListScreen(
    onOpenRequest: (conversationId: String, title: String) -> Unit,
    onBack: () -> Unit,
    viewModel: MessageRequestsViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    // Back from a decision: the decided request leaves the list.
    LifecycleResumeEffect(viewModel) {
        if (!state.loading) viewModel.refresh()
        onPauseOrDispose { }
    }
    BackHandler(enabled = state.hidden) { viewModel.showAll() }
    UsScaffold(
        topBar = {
            UsTopBar(
                title = if (state.hidden) "Hidden requests" else "Message requests",
                onBack = if (state.hidden) viewModel::showAll else onBack,
            )
        },
        applyPageGutter = false,
    ) { padding ->
        Column(modifier = Modifier.padding(padding).fillMaxSize()) {
            RequestsFolderBody(state = state, viewModel = viewModel, onOpenRequest = onOpenRequest)
        }
    }
}

@Composable
private fun RequestsFolderBody(
    state: RequestsFolderState,
    viewModel: MessageRequestsViewModel,
    onOpenRequest: (conversationId: String, title: String) -> Unit,
) {
    when {
        state.loading && state.rows.isEmpty() -> UsLoadingState(label = "Loading requests")
        state.failed && state.rows.isEmpty() -> UsErrorState(
            message = "Requests couldn't be loaded.",
            onRetry = viewModel::refresh,
        )
        else -> LazyColumn(
            modifier = Modifier
                .fillMaxSize()
                .testTag(if (state.hidden) "chat_requests_filtered_list" else "chat_requests"),
        ) {
            if (state.hidden) {
                item(key = "hidden_note") {
                    Text(
                        "Requests that look risky wait here: brand-new accounts, links, or words you filter. " +
                            "The sender isn't told.",
                        style = MaterialTheme.typography.bodySmall,
                        color = UsTheme.extended.textMuted,
                        modifier = Modifier.padding(
                            horizontal = UsTheme.spacing.pageHorizontal,
                            vertical = UsTheme.spacing.s,
                        ),
                    )
                }
            }
            if (state.rows.isEmpty()) {
                item(key = "empty") {
                    if (state.hidden) {
                        UsEmptyState(title = "No hidden requests")
                    } else {
                        UsEmptyState(
                            title = "No message requests",
                            detail = "When someone you don't follow messages you, it waits here until you decide.",
                        )
                    }
                }
            }
            items(state.rows, key = { it.id }) { request ->
                val title = request.requestTitle(state.viewerId)
                ConversationRow(
                    conversation = request,
                    title = title,
                    viewerId = state.viewerId,
                    onClick = { onOpenRequest(request.id, title) },
                )
            }
            if (state.next != null) {
                item(key = "more") {
                    // Reaching the foot asks for the next page.
                    LaunchedEffect(state.rows.size, state.next) { viewModel.loadMore() }
                    Box(
                        contentAlignment = Alignment.Center,
                        modifier = Modifier.fillMaxWidth().padding(UsTheme.spacing.l),
                    ) {
                        CircularProgressIndicator(color = UsTheme.extended.chatAccent)
                    }
                }
            }
            if (!state.hidden && state.hiddenAvailable && state.next == null) {
                item(key = "hidden_entry") {
                    HiddenRequestsEntry(
                        count = state.hiddenCount,
                        capped = state.hiddenCapped,
                        onClick = viewModel::showHidden,
                    )
                }
            }
        }
    }
}

/** "Hidden requests", at the foot of the list, with how many wait there. */
@Composable
private fun HiddenRequestsEntry(count: Int?, capped: Boolean, onClick: () -> Unit) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.l)
            .testTag("chat_requests_hidden_entry"),
    ) {
        Icon(UsIcons.EyeOff, contentDescription = null, tint = UsTheme.extended.textMuted)
        Text(
            "Hidden requests",
            style = MaterialTheme.typography.bodyLarge,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.weight(1f),
        )
        hiddenCountLabel(count, capped)?.let {
            Text(it, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textMuted)
        }
        Icon(UsIcons.ChevronRight, contentDescription = null, tint = UsTheme.extended.textMuted)
    }
}

/**
 * The Hidden requests entry's number: "3", nothing when none wait (or the
 * count is unknown), and "3+" when the server stopped counting — the scan
 * that capped the badge at 999+ may have more hidden ones beyond it, so the
 * hidden number is "at least" rather than a made-up 999+.
 */
internal fun hiddenCountLabel(count: Int?, capped: Boolean): String? = when {
    count == null || count <= 0 -> null
    capped -> "$count+"
    else -> requestCountLabel(count, capped = false)
}
