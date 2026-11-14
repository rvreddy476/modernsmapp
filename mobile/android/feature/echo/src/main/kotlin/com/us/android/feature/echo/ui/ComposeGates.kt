package com.us.android.feature.echo.ui

import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.feature.echo.data.EchoRepository
import com.us.android.feature.echo.data.Eligibility
import com.us.android.feature.echo.data.Note
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

@Immutable
data class ComposeGateState(
    val checking: Boolean = false,
    val openComposer: ComposeRequest? = null,
    val blocked: ComposeGate.Blocked? = null,
    val message: String? = null,
)

/**
 * The eligibility read in front of every composer a list can open: a quote
 * or a new post needs `can_post` (Premium); a reply needs `can_reply` and
 * the note's own `viewer_can_reply`. A refused viewer gets the sheet; a
 * failed read says why and opens nothing (fail closed).
 */
class ComposeGateController(private val scope: CoroutineScope, private val repository: EchoRepository) {
    private val _state = MutableStateFlow(ComposeGateState())
    val state: StateFlow<ComposeGateState> = _state.asStateFlow()

    fun compose(thread: Boolean = false) = gate(ComposeRequest(ComposeMode.NEW, thread = thread), ::postGate)

    fun quote(note: Note) = gate(ComposeRequest(ComposeMode.QUOTE, note.id), ::postGate)

    fun reply(note: Note) = gate(ComposeRequest(ComposeMode.REPLY, note.id)) { replyGate(it, note) }

    private fun gate(request: ComposeRequest, rule: (Eligibility) -> ComposeGate) {
        if (_state.value.checking) return
        _state.update { it.copy(checking = true) }
        scope.launch {
            val gate = readGate(repository, rule)
            _state.update {
                when (gate) {
                    ComposeGate.Open -> it.copy(checking = false, openComposer = request)
                    is ComposeGate.Blocked -> it.copy(checking = false, blocked = gate)
                    is ComposeGate.Failed -> it.copy(checking = false, message = gate.message)
                }
            }
        }
    }

    fun opened() = _state.update { it.copy(openComposer = null) }

    fun dismissBlocked() = _state.update { it.copy(blocked = null) }

    fun messageShown() = _state.update { it.copy(message = null) }
}

/** Navigates when [gate] opens, draws the Premium / eligibility sheet when it refuses. */
@Composable
fun ComposeGateHost(gate: ComposeGateController, destinations: EchoDestinations) {
    val state by gate.state.collectAsStateWithLifecycle()
    LaunchedEffect(state.openComposer) {
        state.openComposer?.let {
            gate.opened()
            destinations.onCompose(it)
        }
    }
    state.blocked?.let {
        EligibilitySheet(
            blocked = it,
            onDismiss = gate::dismissBlocked,
            premiumEntryShown = destinations.premiumEntryShown,
            onGetPremium = destinations.onGetPremium,
        )
    }
    val message = remember(state.message) { state.message?.let { UsMessage(it) } }
    EchoMessageHost(message = message, onDismiss = gate::messageShown)
}
