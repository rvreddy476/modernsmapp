package com.us.android.feature.dating.safety

import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.feature.dating.data.DatingResult
import com.us.android.feature.dating.home.reportFailure
import com.us.android.feature.dating.ui.DatingScreen
import com.us.android.feature.dating.ui.LoadingPane
import com.us.android.feature.dating.ui.MessagePane
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/*
 * Reporting someone from outside Dating's own screens: the chat of a Pulse
 * match, after "Did this bother you?" was answered Yes and the server offered
 * the report flow (mechanic M13). `:app` opens this route with the sender and
 * the message; it is the ordinary report sheet, which also blocks them.
 */

enum class ReportPersonPhase { EDITING, SENDING, SENT, CLOSED }

data class ReportPersonUi(
    val phase: ReportPersonPhase = ReportPersonPhase.EDITING,
    val draft: ReportDraft,
    /** Their first name when the opening screen knew it. */
    val name: String?,
    val message: UsMessage? = null,
)

/** The words. Our own. */
object ReportPersonCopy {
    const val TITLE = "Report"
    const val SENT_TITLE = "Thanks for telling us"
    const val SENT_BODY = "We've blocked them for you, and our safety team will look into it. They won't know it was you."
    const val DONE = "Done"
    const val CLOSED_TITLE = "Nothing was sent"
    const val CLOSED_BODY = "You can report them any time from your match."
    const val BACK = "Back"
}

@HiltViewModel
class ReportPersonViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val safety: SafetyActions,
) : ViewModel() {

    private val _state = MutableStateFlow(
        ReportPersonUi(
            draft = ReportDraft(
                targetId = savedStateHandle.get<String>(ARG_USER_ID).orEmpty(),
                messageIds = listOfNotNull(savedStateHandle.get<String>(ARG_MESSAGE_ID)?.trim()?.takeIf { it.isNotEmpty() }),
            ),
            name = savedStateHandle.get<String>(ARG_NAME)?.trim()?.takeIf { it.isNotEmpty() },
        ),
    )
    val state: StateFlow<ReportPersonUi> = _state.asStateFlow()

    fun dismissMessage() = _state.update { it.copy(message = null) }

    /** The sheet was closed without sending. */
    fun close() = _state.update { if (it.phase == ReportPersonPhase.EDITING) it.copy(phase = ReportPersonPhase.CLOSED) else it }

    /** Opens the sheet again after it was closed. */
    fun reopen() = _state.update { if (it.phase == ReportPersonPhase.CLOSED) it.copy(phase = ReportPersonPhase.EDITING) else it }

    fun submit(draft: ReportDraft) {
        if (_state.value.phase != ReportPersonPhase.EDITING || draft.targetId.isBlank()) return
        _state.update { it.copy(phase = ReportPersonPhase.SENDING, draft = draft, message = null) }
        viewModelScope.launch {
            when (val result = safety.report(draft)) {
                is DatingResult.Success -> _state.update { it.copy(phase = ReportPersonPhase.SENT) }
                // The sheet comes back with what they chose, and says why.
                is DatingResult.Failure -> _state.update { it.copy(phase = ReportPersonPhase.EDITING, message = reportFailure(result.error)) }
            }
        }
    }

    companion object {
        const val ARG_USER_ID = "userId"
        const val ARG_MESSAGE_ID = "messageId"
        const val ARG_NAME = "name"
    }
}

@Composable
fun ReportPersonScreen(onBack: () -> Unit, viewModel: ReportPersonViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    DatingScreen(
        title = ReportPersonCopy.TITLE,
        onBack = onBack,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
    ) {
        when (state.phase) {
            ReportPersonPhase.EDITING -> ReportSheet(
                initial = state.draft,
                name = state.name,
                onSubmit = viewModel::submit,
                onDismiss = viewModel::close,
            )
            ReportPersonPhase.SENDING -> LoadingPane()
            ReportPersonPhase.SENT -> MessagePane(
                title = ReportPersonCopy.SENT_TITLE,
                body = ReportPersonCopy.SENT_BODY,
                icon = UsIcons.HeartHandshake,
                primaryLabel = ReportPersonCopy.DONE,
                onPrimary = onBack,
            )
            ReportPersonPhase.CLOSED -> MessagePane(
                title = ReportPersonCopy.CLOSED_TITLE,
                body = ReportPersonCopy.CLOSED_BODY,
                primaryLabel = ReportPersonCopy.BACK,
                onPrimary = onBack,
                secondaryLabel = ReportPersonCopy.TITLE,
                onSecondary = viewModel::reopen,
            )
        }
    }
}
