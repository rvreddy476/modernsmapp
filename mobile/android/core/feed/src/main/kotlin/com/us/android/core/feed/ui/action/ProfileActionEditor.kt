package com.us.android.core.feed.ui.action

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.ChannelRepository
import com.us.android.core.feed.data.ChannelState
import com.us.android.core.feed.data.action.ActionRepository
import com.us.android.core.feed.data.action.actionProblem
import com.us.android.core.model.ActionButtons
import com.us.android.core.model.ActionDraft
import com.us.android.core.model.ActionPatch
import com.us.android.core.model.ActionProblem
import com.us.android.core.model.ActionStats
import com.us.android.core.model.ActionStatus
import com.us.android.core.ui.UsSettingsSection
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** The profile Action button's editor, as it draws. */
data class ProfileActionUiState(
    val loading: Boolean = true,
    /** The channel could not be read: nothing to edit yet. */
    val unavailable: Boolean = false,
    /** As saved (the owner's `channels/me` copy, url included). */
    val base: ActionDraft? = null,
    /** As the owner has it now; null = none. */
    val draft: ActionDraft? = null,
    /** The saved button's status when it is not live. */
    val status: ActionStatus? = null,
    /** Why it is not live, when the server said (`status_reason`). */
    val reason: String? = null,
    /** Taps on the saved button (`GET /v1/channels/me/action/stats`); null until read, or when it cannot be. */
    val taps: ActionStats? = null,
    val saving: Boolean = false,
    val saved: Boolean = false,
    /** The server's refusal of the button, in our words. */
    val problem: ActionProblem? = null,
    /** Anything else that went wrong. */
    val error: String? = null,
) {
    val dirty: Boolean get() = ActionButtons.patch(base, draft) != ActionPatch.Unchanged

    val canSave: Boolean get() = !loading && !saving && dirty && ActionButtons.draftProblem(draft) == null
}

/**
 * The profile Action button (2026-10-23), next to Follow and Message:
 * `PATCH /v1/channels/me {action}` — `{label, url}` to set, `null` to remove,
 * left out when unchanged. Every account has a channel (default channel on
 * sign-up), so the button is edited on the owner's channel.
 */
@HiltViewModel
class ProfileActionEditorViewModel @Inject constructor(
    private val channels: ChannelRepository,
    private val actions: ActionRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(ProfileActionUiState())
    val state: StateFlow<ProfileActionUiState> = _state.asStateFlow()

    init {
        viewModelScope.launch {
            when (val own = channels.ensureLoaded()) {
                is ChannelState.Present -> {
                    val saved = own.channel.action
                    val draft = ActionButtons.draftFrom(saved)
                    _state.value = ProfileActionUiState(
                        loading = false,
                        base = draft,
                        draft = draft,
                        status = saved?.status?.takeIf { it != ActionStatus.ACTIVE },
                        reason = saved?.statusReason,
                    )
                    if (saved != null) {
                        val taps = actions.profileStats()
                        _state.update { it.copy(taps = taps) }
                    }
                }
                else -> _state.value = ProfileActionUiState(loading = false, unavailable = true)
            }
        }
    }

    fun change(next: ActionDraft?) = _state.update { it.copy(draft = next, problem = null, error = null, saved = false) }

    fun save() {
        val now = _state.value
        if (!now.canSave) return
        val patch = ActionButtons.patch(now.base, now.draft)
        _state.update { it.copy(saving = true, problem = null, error = null) }
        viewModelScope.launch {
            when (val result = channels.update(name = null, handle = null, about = null, action = patch)) {
                is AppResult.Success -> {
                    val saved = result.data.action
                    _state.update {
                        it.copy(
                            saving = false,
                            saved = true,
                            base = it.draft,
                            status = saved?.status?.takeIf { s -> s != ActionStatus.ACTIVE },
                            reason = saved?.statusReason,
                        )
                    }
                }
                is AppResult.Failure -> {
                    val problem = result.error.actionProblem()
                    _state.update {
                        it.copy(
                            saving = false,
                            problem = problem,
                            error = if (problem == null) saveError(result.error) else null,
                        )
                    }
                }
            }
        }
    }

    private fun saveError(error: AppError): String = when (error) {
        is AppError.NoNetwork, is AppError.Timeout -> "You're offline. Try again when you're connected."
        else -> "We couldn't save your action button. Try again."
    }
}

/** "Action button" on the Edit profile screen: the editor, its status, and its own Save. */
@Composable
fun ProfileActionSection(
    modifier: Modifier = Modifier,
    viewModel: ProfileActionEditorViewModel = hiltViewModel(key = "profile-action"),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    if (state.unavailable) return
    UsSettingsSection(title = "Action button", modifier = modifier.testTag("profile_action_section")) {
        Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
            ActionEditor(
                value = state.draft,
                onChange = viewModel::change,
                surface = ActionSurface.Profile,
                savedStatus = state.status.takeIf { state.draft == state.base },
                savedReason = state.reason,
                serverProblem = state.problem,
                enabled = !state.loading && !state.saving,
            )
            state.taps?.takeIf { state.base != null }?.let { taps ->
                Text(
                    text = "Action button: ${taps.tapsText}",
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                    modifier = Modifier.testTag("profile_action_taps"),
                )
            }
            state.error?.let {
                Text(text = it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
            }
            if (state.saved && !state.dirty) {
                Text(
                    text = if (state.base == null) "Action button removed." else "Action button saved.",
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.statusSuccess,
                )
            }
            if (state.dirty) {
                UsSecondaryButton(
                    text = if (state.saving) "Saving…" else "Save action button",
                    onClick = viewModel::save,
                    enabled = state.canSave,
                    modifier = Modifier.fillMaxWidth().testTag("profile_action_save"),
                )
            }
        }
    }
}
