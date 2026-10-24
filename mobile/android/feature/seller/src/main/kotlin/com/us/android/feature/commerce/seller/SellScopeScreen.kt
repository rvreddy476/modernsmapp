package com.us.android.feature.commerce.seller

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
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
import com.us.android.core.commerce.model.SellScope
import com.us.android.core.commerce.model.SellScopeDraft
import com.us.android.core.commerce.model.SellScopeField
import com.us.android.core.commerce.model.SellScopeSaved
import com.us.android.core.commerce.model.normaliseTaxId
import com.us.android.core.commerce.model.sellScopeChoice
import com.us.android.core.commerce.model.sellScopeProblems
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.commerce.repository.CommerceResult
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsChoice
import com.us.android.core.designsystem.component.UsChoiceRow
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.commerce.ui.CommerceNotice
import com.us.android.feature.commerce.ui.MSellerPageBar
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * "What will you sell?" — the onboarding step of migration 045 (founder,
 * 24 Oct 2026). All categories need a GSTIN; books only need a PAN and the
 * business name, and later a business address and a proof of address.
 *
 * The local checks show only after a first Save ([attempted]), so a seller
 * is not told off for a field they have not reached; the server's own
 * refusal lands under the field it names ([serverErrors]).
 */
data class SellScopeUiState(
    val draft: SellScopeDraft = SellScopeDraft(),
    val attempted: Boolean = false,
    val saving: Boolean = false,
    val serverErrors: Map<SellScopeField, String> = emptyMap(),
    val error: String? = null,
    val saved: SellScopeSaved? = null,
) {
    fun errorFor(field: SellScopeField): String? =
        serverErrors[field] ?: if (attempted) sellScopeProblems(draft)[field] else null
}

@HiltViewModel
class SellScopeViewModel @Inject constructor(
    private val repo: CommerceRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(SellScopeUiState())
    val state: StateFlow<SellScopeUiState> = _state.asStateFlow()

    init {
        // A shop that chose before starts on its choice. A read that fails
        // leaves the form on its default, which the seller can change.
        viewModelScope.launch {
            val view = repo.sellScope() as? CommerceResult.Success ?: return@launch
            val scope = view.value.scope ?: return@launch
            _state.update { if (it.saved == null) it.copy(draft = it.draft.copy(scope = scope)) else it }
        }
    }

    fun choose(scope: SellScope) = edit { it.copy(scope = scope) }

    fun setGstin(raw: String) = edit { it.copy(gstin = normaliseTaxId(raw).take(GSTIN_LENGTH)) }

    fun setPan(raw: String) = edit { it.copy(pan = normaliseTaxId(raw).take(PAN_LENGTH)) }

    fun setBusinessName(raw: String) = edit { it.copy(businessName = raw.take(BUSINESS_NAME_LIMIT)) }

    fun save() {
        val current = _state.value
        if (current.saving) return
        val draft = current.draft
        if (sellScopeProblems(draft).isNotEmpty()) {
            _state.value = current.copy(attempted = true)
            return
        }
        _state.value = current.copy(attempted = true, saving = true, error = null, serverErrors = emptyMap())
        viewModelScope.launch {
            when (val r = repo.saveSellScope(sellScopeChoice(draft))) {
                is CommerceResult.Failure -> {
                    val refusal = sellScopeRefusal(r.error)
                    _state.update {
                        it.copy(
                            saving = false,
                            serverErrors = refusal.field?.let { f -> mapOf(f to refusal.message) }.orEmpty(),
                            error = refusal.message.takeIf { refusal.field == null },
                        )
                    }
                }

                is CommerceResult.Success -> _state.update { it.copy(saving = false, saved = r.value) }
            }
        }
    }

    private fun edit(transform: (SellScopeDraft) -> SellScopeDraft) = _state.update {
        it.copy(draft = transform(it.draft), serverErrors = emptyMap(), error = null, saved = null)
    }

    private companion object {
        const val GSTIN_LENGTH = 15
        const val PAN_LENGTH = 10
        const val BUSINESS_NAME_LIMIT = 200
    }
}

@Composable
fun SellScopeScreen(
    onBack: () -> Unit,
    onDone: () -> Unit,
    viewModel: SellScopeViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    UsScaffold(topBar = { MSellerPageBar(title = "What will you sell?", onBack = onBack) }) { padding ->
        SellScopeContent(
            state = state,
            modifier = Modifier
                .padding(padding)
                .verticalScroll(rememberScrollState()),
            onChoose = viewModel::choose,
            onGstin = viewModel::setGstin,
            onPan = viewModel::setPan,
            onBusinessName = viewModel::setBusinessName,
            onSave = viewModel::save,
            onDone = onDone,
        )
    }
}

@Suppress("LongParameterList")
@Composable
internal fun SellScopeContent(
    state: SellScopeUiState,
    modifier: Modifier = Modifier,
    onChoose: (SellScope) -> Unit,
    onGstin: (String) -> Unit,
    onPan: (String) -> Unit,
    onBusinessName: (String) -> Unit,
    onSave: () -> Unit,
    onDone: () -> Unit,
) {
    val draft = state.draft
    Column(
        modifier = modifier
            .padding(vertical = UsTheme.spacing.m)
            .testTag("mseller_sell_scope"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        UsChoiceRow(
            options = SellScope.entries.map { UsChoice(it, it.label) },
            selected = draft.scope,
            onSelect = { it?.let(onChoose) },
            label = "What will you sell?",
            enabled = !state.saving,
            allowDeselect = false,
        )
        Text(
            text = draft.scope.help,
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textSecondary,
        )

        when (draft.scope) {
            SellScope.ALL_CATEGORIES -> UsTextField(
                value = draft.gstin,
                onValueChange = onGstin,
                label = "GSTIN",
                placeholder = "15 characters, like 29AAACM1234A1Z4",
                errorText = state.errorFor(SellScopeField.GSTIN),
                enabled = !state.saving,
                modifier = Modifier.testTag("mseller_sell_scope:gstin"),
            )

            SellScope.BOOKS_ONLY -> {
                UsTextField(
                    value = draft.pan,
                    onValueChange = onPan,
                    label = "PAN",
                    placeholder = "Like ABCPE1234F",
                    errorText = state.errorFor(SellScopeField.PAN),
                    enabled = !state.saving,
                    modifier = Modifier.testTag("mseller_sell_scope:pan"),
                )
                CommerceNotice(
                    text = "You will also add your business address and one proof of address: a voter ID " +
                        "or passport (both sides), or an electricity bill, telephone bill or bank statement.",
                )
            }
        }

        UsTextField(
            value = draft.businessName,
            onValueChange = onBusinessName,
            label = if (draft.scope == SellScope.BOOKS_ONLY) "Business name" else "Business name (optional)",
            errorText = state.errorFor(SellScopeField.BUSINESS_NAME),
            enabled = !state.saving,
            modifier = Modifier.testTag("mseller_sell_scope:business_name"),
        )

        state.error?.let { error ->
            Text(
                text = error,
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.statusDanger,
            )
        }

        val saved = state.saved
        if (saved == null) {
            UsButton(
                text = "Save",
                onClick = onSave,
                enabled = !state.saving,
                loading = state.saving,
                modifier = Modifier.fillMaxWidth(),
            )
        } else {
            CommerceNotice(
                text = listOfNotNull("Saved: ${saved.scope?.label ?: draft.scope.label}.", sellScopeSavedLine(saved))
                    .joinToString(" "),
            )
            UsButton(text = "Continue", onClick = onDone, modifier = Modifier.fillMaxWidth())
        }
    }
}
