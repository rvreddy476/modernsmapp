package com.us.android.feature.doorsteppro.onboarding

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.LifecycleEventEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.doorsteppro.data.DoorstepProRepository
import com.us.android.feature.doorsteppro.data.ProResult
import com.us.android.feature.doorsteppro.data.userMessage
import com.us.android.feature.doorsteppro.domain.Checklist
import com.us.android.feature.doorsteppro.domain.ChecklistItem
import com.us.android.feature.doorsteppro.domain.OnboardingChecklist
import com.us.android.feature.doorsteppro.domain.OnboardingStep
import com.us.android.feature.doorsteppro.domain.ProStatus
import com.us.android.feature.doorsteppro.domain.StepState
import com.us.android.feature.doorsteppro.store.OnboardingMemory
import com.us.android.feature.doorsteppro.ui.ActionRow
import com.us.android.feature.doorsteppro.ui.CardHeading
import com.us.android.feature.doorsteppro.ui.InfoNote
import com.us.android.feature.doorsteppro.ui.LoadingPane
import com.us.android.feature.doorsteppro.ui.MessagePane
import com.us.android.feature.doorsteppro.ui.Pill
import com.us.android.feature.doorsteppro.ui.ProCard
import com.us.android.feature.doorsteppro.ui.ProScreen
import com.us.android.feature.doorsteppro.ui.SectionLabel
import com.us.android.feature.doorsteppro.ui.Tone
import com.us.android.feature.doorsteppro.ui.humanise
import com.us.android.feature.doorsteppro.ui.label
import com.us.android.feature.doorsteppro.ui.listPadding
import com.us.android.feature.doorsteppro.ui.tone
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

data class OnboardingUiState(
    val loading: Boolean = true,
    val checklist: Checklist? = null,
    val error: String? = null,
    val message: UsMessage? = null,
)

/**
 * The checklist from `GET /pro/readiness`, re-read every time the screen comes
 * back (a step screen just saved, or an admin decided while the app slept).
 * A step that the server now counts as done is no longer "submitted for
 * review" on this device.
 */
@HiltViewModel
class OnboardingViewModel @Inject constructor(
    private val repository: DoorstepProRepository,
    private val memory: OnboardingMemory,
) : ViewModel() {

    private val _state = MutableStateFlow(OnboardingUiState())
    val state: StateFlow<OnboardingUiState> = _state.asStateFlow()

    fun refresh() {
        viewModelScope.launch {
            when (val result = repository.readiness()) {
                is ProResult.Success -> {
                    val readiness = result.value
                    readiness.completedSteps.mapNotNull(OnboardingStep::of).forEach(memory::clearSubmitted)
                    val checklist = OnboardingChecklist.of(readiness, memory.submittedForReview())
                    _state.update { it.copy(loading = false, checklist = checklist, error = null) }
                }
                is ProResult.Failure -> _state.update {
                    it.copy(loading = false, error = if (it.checklist == null) result.error.userMessage() else null)
                }
            }
        }
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }
}

@Composable
fun OnboardingScreen(
    onBack: (() -> Unit)?,
    onOpenStep: (OnboardingStep) -> Unit,
    onOpenHome: () -> Unit,
    onOpenAccount: () -> Unit,
    viewModel: OnboardingViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LifecycleEventEffect(Lifecycle.Event.ON_RESUME) { viewModel.refresh() }
    ProScreen(
        title = "Get verified",
        onBack = onBack,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
        actions = {
            IconButton(onClick = onOpenAccount) {
                Icon(UsIcons.Profile, contentDescription = "Account", tint = UsTheme.extended.textMuted)
            }
        },
    ) { padding ->
        val checklist = state.checklist
        when {
            state.loading && checklist == null -> LoadingPane()
            checklist == null -> MessagePane(
                title = "Couldn't load your checklist",
                body = state.error.orEmpty(),
                primaryLabel = "Try again",
                onPrimary = viewModel::refresh,
            )
            else -> LazyColumn(modifier = Modifier.fillMaxSize(), contentPadding = listPadding(padding)) {
                item { StatusCard(checklist, onOpenHome) }
                item { SectionLabel("Your checklist") }
                items(checklist.items, key = { it.step.wire }) { item ->
                    StepRow(item, onClick = { onOpenStep(item.step) }, modifier = Modifier.padding(bottom = UsTheme.spacing.m))
                }
                items(checklist.unknownMissing) { wire ->
                    ActionRow(
                        icon = UsIcons.CircleHelp,
                        title = humanise(wire),
                        subtitle = "Update Doorstep Pro to finish this step",
                        modifier = Modifier.padding(bottom = UsTheme.spacing.m),
                    )
                }
            }
        }
    }
}

@Composable
private fun StatusCard(checklist: Checklist, onOpenHome: () -> Unit) {
    ProCard {
        CardHeading(
            title = when {
                checklist.status == ProStatus.APPROVED -> "You're approved"
                checklist.waitingForReview -> "Doorstep is reviewing your documents"
                else -> "${checklist.requiredLeft} of ${checklist.requiredTotal} steps left"
            },
            subtitle = when (checklist.status) {
                ProStatus.APPROVED -> "Go on duty to start receiving jobs."
                ProStatus.REJECTED -> "Your application wasn't approved. Check the steps marked below, or contact support."
                ProStatus.SUSPENDED -> "Your account is paused while Doorstep looks into something."
                ProStatus.BLOCKED -> "This account can no longer take Doorstep jobs."
                else -> if (checklist.waitingForReview) {
                    "We'll notify you when it's done — usually within two working days."
                } else {
                    "Finish each step. Doorstep reviews your police certificate and any trade certificates."
                }
            },
        )
        Pill(checklist.status.label(), checklist.status.tone())
        if (checklist.requiredTotal > 0) {
            val done = checklist.requiredTotal - checklist.requiredLeft
            LinearProgressIndicator(
                progress = { done.toFloat() / checklist.requiredTotal },
                modifier = Modifier.fillMaxWidth(),
                color = UsTheme.extended.accentSolid,
                trackColor = UsTheme.extended.borderSubtle,
            )
        }
        val next = checklist.next
        if (checklist.status == ProStatus.APPROVED) {
            UsButton(text = "Go to home", onClick = onOpenHome, modifier = Modifier.fillMaxWidth())
        } else if (next != null) {
            Column {
                Text("Next: ${next.title}", style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textSecondary)
            }
        }
        if (!checklist.canGoOnDuty && checklist.status == ProStatus.APPROVED) {
            InfoNote("A step needs attention before you can go on duty.", tone = Tone.Warning)
        }
    }
}

@Composable
private fun StepRow(item: ChecklistItem, onClick: () -> Unit, modifier: Modifier = Modifier) {
    val (label, tone) = when (item.state) {
        StepState.DONE -> "Done" to Tone.Positive
        StepState.TO_DO -> "To do" to Tone.Accent
        StepState.IN_REVIEW -> "In review" to Tone.Warning
        StepState.WAITING -> "After Aadhaar" to Tone.Neutral
        StepState.OPTIONAL -> "Optional" to Tone.Neutral
    }
    ActionRow(
        icon = when (item.state) {
            StepState.DONE -> UsIcons.Check
            StepState.IN_REVIEW -> UsIcons.Clock
            StepState.WAITING -> UsIcons.Lock
            else -> UsIcons.ChevronRight
        },
        title = item.step.title,
        subtitle = item.step.blurb,
        enabled = item.actionable,
        onClick = if (item.actionable) onClick else null,
        modifier = modifier,
        trailing = { Pill(label, tone) },
    )
}
