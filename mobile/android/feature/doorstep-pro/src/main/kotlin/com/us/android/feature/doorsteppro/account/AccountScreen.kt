package com.us.android.feature.doorsteppro.account

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.doorsteppro.data.DoorstepProRepository
import com.us.android.feature.doorsteppro.data.ProResult
import com.us.android.feature.doorsteppro.data.ProfessionalDto
import com.us.android.feature.doorsteppro.domain.OnboardingStep
import com.us.android.feature.doorsteppro.domain.ProStatus
import com.us.android.feature.doorsteppro.root.ProSession
import com.us.android.feature.doorsteppro.store.OnboardingMemory
import com.us.android.feature.doorsteppro.ui.ActionRow
import com.us.android.feature.doorsteppro.ui.CardHeading
import com.us.android.feature.doorsteppro.ui.LabeledValue
import com.us.android.feature.doorsteppro.ui.Pill
import com.us.android.feature.doorsteppro.ui.ProCard
import com.us.android.feature.doorsteppro.ui.ProScreen
import com.us.android.feature.doorsteppro.ui.SectionLabel
import com.us.android.feature.doorsteppro.ui.humanise
import com.us.android.feature.doorsteppro.ui.label
import com.us.android.feature.doorsteppro.ui.listPadding
import com.us.android.feature.doorsteppro.ui.tone
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import java.util.Locale
import javax.inject.Inject

data class AccountUiState(val professional: ProfessionalDto? = null, val bank: String? = null, val radiusKm: Int? = null)

/** The professional's own record and the settings they can change after approval. */
@HiltViewModel
class AccountViewModel @Inject constructor(
    private val repository: DoorstepProRepository,
    private val memory: OnboardingMemory,
    session: ProSession,
) : ViewModel() {
    private val _state = MutableStateFlow(AccountUiState(session.professional.value, memory.bankSummary(), memory.radiusKm()))
    val state: StateFlow<AccountUiState> = _state.asStateFlow()

    init {
        viewModelScope.launch {
            (repository.me() as? ProResult.Success)?.value?.let { me ->
                _state.value = AccountUiState(me, memory.bankSummary(), memory.radiusKm())
            }
        }
    }
}

@Composable
fun AccountScreen(
    onBack: () -> Unit,
    onOpenStep: (OnboardingStep) -> Unit,
    onOpenChecklist: () -> Unit,
    onSignOut: () -> Unit,
    viewModel: AccountViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    ProScreen(title = "Account", onBack = onBack) { padding ->
        LazyColumn(modifier = Modifier.fillMaxSize(), contentPadding = listPadding(padding), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
            state.professional?.let { pro ->
                item {
                    ProCard {
                        val status = ProStatus.of(pro.status)
                        CardHeading(pro.displayName, "Doorstep professional · ${pro.cityCode}")
                        Pill(status.label(), status.tone())
                        pro.gender?.let { LabeledValue("Gender (Aadhaar)", humanise(it)) }
                        LabeledValue("Jobs completed", pro.jobsCompleted.toString())
                        LabeledValue(
                            "Rating",
                            pro.ratingAvg?.let { String.format(Locale.ENGLISH, "%.1f from %d", it, pro.ratingCount) } ?: "No ratings yet",
                        )
                        LabeledValue("Jobs a day, at most", pro.maxJobsPerDay.toString())
                    }
                }
            }
            item { SectionLabel("Work settings") }
            item { ActionRow(UsIcons.Clock, "Working hours and days off", null, onClick = { onOpenStep(OnboardingStep.WEEKLY_HOURS) }) }
            item {
                ActionRow(
                    UsIcons.MapPin,
                    "Service area",
                    state.radiusKm?.let { "$it km from your home point" },
                    onClick = { onOpenStep(OnboardingStep.SERVICE_AREA) },
                )
            }
            item { ActionRow(UsIcons.Wrench, "Skills and trade certificates", null, onClick = { onOpenStep(OnboardingStep.SKILLS) }) }
            item { ActionRow(UsIcons.CreditCard, "Bank account", state.bank, onClick = { onOpenStep(OnboardingStep.BANK) }) }
            item { ActionRow(UsIcons.ShieldAlert, "Police clearance certificate", "Renew before it expires", onClick = { onOpenStep(OnboardingStep.POLICE_CERTIFICATE) }) }
            item { ActionRow(UsIcons.Check, "Verification checklist", null, onClick = onOpenChecklist) }
            item { UsSecondaryButton(text = "Sign out", onClick = onSignOut, modifier = Modifier.fillMaxWidth()) }
        }
    }
}
