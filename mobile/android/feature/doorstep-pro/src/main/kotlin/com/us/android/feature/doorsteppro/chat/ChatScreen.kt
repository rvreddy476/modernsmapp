package com.us.android.feature.doorsteppro.chat

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.doorsteppro.data.DoorstepProRepository
import com.us.android.feature.doorsteppro.data.MessageDto
import com.us.android.feature.doorsteppro.data.ProCodes
import com.us.android.feature.doorsteppro.data.ProResult
import com.us.android.feature.doorsteppro.data.code
import com.us.android.feature.doorsteppro.navigation.requireArg
import com.us.android.feature.doorsteppro.ui.InfoNote
import com.us.android.feature.doorsteppro.ui.LoadingPane
import com.us.android.feature.doorsteppro.ui.ProScreen
import com.us.android.feature.doorsteppro.ui.asMessage
import com.us.android.feature.doorsteppro.ui.slotTimeText
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import javax.inject.Inject

data class ChatUiState(
    val loading: Boolean = true,
    val messages: List<MessageDto> = emptyList(),
    val nextCursor: String? = null,
    val loadingMore: Boolean = false,
    val open: Boolean = true,
    val draft: String = "",
    val sending: Boolean = false,
    val message: UsMessage? = null,
)

/**
 * Chat with the customer, from acceptance until two hours after the job
 * (`GET/POST /pro/jobs/{id}/messages`). No phone numbers: masked calling stays
 * out, as in rider and food. Polled every 10 s while open.
 */
@HiltViewModel
class ChatViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val repository: DoorstepProRepository,
) : ViewModel() {

    private val bookingId = savedStateHandle.requireArg("bookingId")

    private val _state = MutableStateFlow(ChatUiState())
    val state: StateFlow<ChatUiState> = _state.asStateFlow()

    init {
        viewModelScope.launch {
            while (isActive) {
                load()
                if (!_state.value.open) break
                delay(POLL_MILLIS)
            }
        }
    }

    private var fetching = false
    private suspend fun load(more: Boolean = false) {
        if (fetching) return
        fetching = true
        _state.update { it.copy(loadingMore = more) }
        try {
            val target = _state.value.messages.size
            var cursor = if (more) _state.value.nextCursor else null
            if (more && cursor == null) return
            val gathered = if (more) _state.value.messages.toMutableList() else mutableListOf()
            do {
                when (val result = repository.messages(bookingId, cursor)) {
                    is ProResult.Success -> {
                        gathered.addAll(result.value.items)
                        cursor = result.value.nextCursor
                        _state.update { it.copy(loading = false, messages = gathered.distinctBy { m -> m.id }.sortedBy(MessageDto::createdAt), nextCursor = cursor, open = result.value.open) }
                        result.value.items.filter { m -> m.senderKind == "customer" && m.readAt == null }.forEach { m -> repository.readMessage(bookingId, m.id) }
                    }
                    is ProResult.Failure -> { _state.update { it.copy(loading = false, message = it.message ?: result.error.asMessage()) }; return }
                }
            } while (!more && cursor != null && gathered.size < target)
        } finally { fetching = false; _state.update { it.copy(loadingMore = false) } }
    }
    fun loadMore() { viewModelScope.launch { load(more = true) } }

    fun onDraft(text: String) = _state.update { it.copy(draft = text.take(MAX_BODY)) }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun send() {
        val body = _state.value.draft.trim()
        if (body.isEmpty() || _state.value.sending) return
        _state.update { it.copy(sending = true) }
        viewModelScope.launch {
            when (val result = repository.sendMessage(bookingId, body)) {
                is ProResult.Success -> _state.update { it.copy(sending = false, draft = "", messages = it.messages + result.value) }
                is ProResult.Failure -> _state.update {
                    it.copy(sending = false, open = result.error.code != ProCodes.CHAT_CLOSED && it.open, message = result.error.asMessage())
                }
            }
        }
    }

    private companion object {
        const val POLL_MILLIS = 10_000L
        const val MAX_BODY = 1_000
    }
}

@Composable
fun ChatScreen(onBack: () -> Unit, viewModel: ChatViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    ProScreen(
        title = "Chat with customer",
        onBack = onBack,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
        bottomBar = {
            if (state.open) {
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .background(UsTheme.extended.bgCanvas)
                        .navigationBarsPadding()
                        .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    UsTextField(value = state.draft, onValueChange = viewModel::onDraft, label = "Message", modifier = Modifier.weight(1f))
                    IconButton(onClick = viewModel::send, enabled = !state.sending && state.draft.isNotBlank()) {
                        Icon(UsIcons.Send, contentDescription = "Send", tint = UsTheme.extended.accentSolid)
                    }
                }
            }
        },
    ) { padding ->
        if (state.loading) {
            LoadingPane()
            return@ProScreen
        }
        LazyColumn(
            modifier = Modifier.fillMaxSize(),
            contentPadding = padding,
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        ) {
            if (!state.open) item { InfoNote("Chat closes two hours after the job.") }
            if (state.nextCursor != null) item { TextButton(onClick = viewModel::loadMore, enabled = !state.loadingMore) { Text(if (state.loadingMore) "Loading…" else "More messages") } }
            if (state.messages.isEmpty() && state.open) item { InfoNote("Say hello, or tell the customer when you'll arrive.") }
            items(state.messages, key = { it.id }) { message -> Bubble(message) }
        }
    }
}

@Composable
private fun Bubble(message: MessageDto) {
    val mine = message.senderKind == "pro"
    val system = message.senderKind == "system"
    Box(modifier = Modifier.fillMaxWidth(), contentAlignment = if (mine) Alignment.CenterEnd else Alignment.CenterStart) {
        Column(
            modifier = Modifier
                .widthIn(max = 300.dp)
                .background(
                    when {
                        system -> UsTheme.extended.bgRaised
                        mine -> UsTheme.extended.accentSolid.copy(alpha = MINE_ALPHA)
                        else -> UsTheme.extended.bgCardSolid
                    },
                    RoundedCornerShape(UsTheme.radii.medium),
                )
                .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.m),
        ) {
            Text(message.body, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textPrimary)
            Text(slotTimeText(message.createdAt), style = MaterialTheme.typography.labelSmall, color = UsTheme.extended.textMuted)
        }
    }
}

private const val MINE_ALPHA = 0.16f
