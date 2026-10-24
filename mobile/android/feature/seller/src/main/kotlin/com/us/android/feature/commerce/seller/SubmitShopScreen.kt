package com.us.android.feature.commerce.seller

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.commerce.model.SellScope
import com.us.android.core.commerce.model.SellerRequirement
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.commerce.repository.CommerceResult
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.commerce.ui.MSellerPageBar
import com.us.android.feature.commerce.ui.describe
import com.us.android.feature.commerce.ui.isRetryable
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed interface SubmitShopUiState {
    data object Loading : SubmitShopUiState

    data class Content(
        val ready: Boolean,
        val missing: List<SellerRequirement>,
        /** What the shop chose to sell (migration 045); null when the server did not say. */
        val scope: SellScope? = null,
        val submitting: Boolean = false,
        val error: String? = null,
    ) : SubmitShopUiState

    data class Failed(val message: String, val retryable: Boolean) : SubmitShopUiState
}

/**
 * Submitting a shop for review.
 *
 * Until this screen existed, `POST /onboarding/submit` was declared in the API
 * layer and called by nothing. A seller opened a shop that stayed `draft`
 * forever, so no seller could ever be approved and nothing they listed could
 * go on sale — the catalogue stayed empty and the buyer journey had nothing to
 * sell.
 */
@HiltViewModel
class SubmitShopViewModel @Inject constructor(
    private val repo: CommerceRepository,
) : ViewModel() {

    private val _state = MutableStateFlow<SubmitShopUiState>(SubmitShopUiState.Loading)
    val state: StateFlow<SubmitShopUiState> = _state.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        _state.value = SubmitShopUiState.Loading
        viewModelScope.launch {
            when (val r = repo.sellerReadiness()) {
                is CommerceResult.Failure ->
                    _state.value = SubmitShopUiState.Failed(
                        r.error.describe(),
                        r.error.isRetryable(),
                    )

                is CommerceResult.Success ->
                    _state.value = SubmitShopUiState.Content(
                        ready = r.value.ready,
                        missing = r.value.missing,
                        scope = r.value.scope,
                    )
            }
        }
    }

    fun submit(onSubmitted: () -> Unit) {
        val current = _state.value as? SubmitShopUiState.Content ?: return
        if (!current.ready || current.submitting) return

        _state.value = current.copy(submitting = true, error = null)
        viewModelScope.launch {
            when (val r = repo.submitSellerApplication()) {
                is CommerceResult.Failure -> {
                    // The server enforces the same rules and may have seen a
                    // change since the checklist loaded. Re-read rather than
                    // guess, so the seller is shown what is actually missing
                    // now instead of the list from a minute ago.
                    _state.value = current.copy(submitting = false, error = r.error.describe())
                    refresh()
                }

                is CommerceResult.Success -> {
                    _state.value = current.copy(submitting = false)
                    onSubmitted()
                }
            }
        }
    }
}

/** The requirements the "What will you sell?" step answers. */
val SELL_SCOPE_STEP_REQUIREMENTS: Set<SellerRequirement> = setOf(
    SellerRequirement.SellScopeNotChosen,
    SellerRequirement.Gstin,
    SellerRequirement.Pan,
    SellerRequirement.BusinessName,
)

/**
 * The checklist's shortcuts, in order: each outstanding requirement the app
 * can resolve, once. Pure, so which button a gap offers is a table test.
 */
enum class SubmitShortcut(val label: String) {
    SELL_SCOPE("Choose what you will sell"),
    BUSINESS_ADDRESS("Add your business address"),
    ADDRESS_PROOF("Send a proof of address"),
    PICKUP_ADDRESS("Add a pickup address"),
    PAYOUT("Add where you get paid"),
    DOCUMENT("Send an identity document"),
}

fun submitShortcuts(missing: List<SellerRequirement>): List<SubmitShortcut> = buildList {
    if (missing.any { it in SELL_SCOPE_STEP_REQUIREMENTS }) add(SubmitShortcut.SELL_SCOPE)
    if (SellerRequirement.BusinessAddress in missing) add(SubmitShortcut.BUSINESS_ADDRESS)
    if (SellerRequirement.AddressProof in missing) add(SubmitShortcut.ADDRESS_PROOF)
    if (SellerRequirement.PickupAddress in missing) add(SubmitShortcut.PICKUP_ADDRESS)
    if (SellerRequirement.PayoutAccount in missing) add(SubmitShortcut.PAYOUT)
    // A proof of address is itself a KYC document, so the books path is sent there instead.
    if (SellerRequirement.KycDocument in missing && SellerRequirement.AddressProof !in missing) add(SubmitShortcut.DOCUMENT)
}

/** Where the checklist's shortcuts go. */
data class SubmitShopActions(
    val openSellScope: () -> Unit,
    val openBusinessAddress: () -> Unit,
    val openAddressProof: () -> Unit,
    val openPickupAddress: () -> Unit,
    val openPayout: () -> Unit,
    val openDocument: () -> Unit,
) {
    fun open(shortcut: SubmitShortcut) = when (shortcut) {
        SubmitShortcut.SELL_SCOPE -> openSellScope()
        SubmitShortcut.BUSINESS_ADDRESS -> openBusinessAddress()
        SubmitShortcut.ADDRESS_PROOF -> openAddressProof()
        SubmitShortcut.PICKUP_ADDRESS -> openPickupAddress()
        SubmitShortcut.PAYOUT -> openPayout()
        SubmitShortcut.DOCUMENT -> openDocument()
    }
}

/** What each outstanding requirement means, in the seller's terms. */
fun SellerRequirement.label(): String = when (this) {
    SellerRequirement.StoreName -> "A name for your shop"
    SellerRequirement.Email -> "A contact email"
    SellerRequirement.PickupAddress -> "A pickup address"
    SellerRequirement.PayoutAccount -> "Where you get paid"
    SellerRequirement.KycDocument -> "An identity document"
    // Per sell scope (migration 045).
    SellerRequirement.SellScopeNotChosen -> "What you will sell"
    SellerRequirement.Gstin -> "Your GSTIN (needed to sell in every category)"
    SellerRequirement.Pan -> "Your PAN"
    SellerRequirement.BusinessName -> "Your business name"
    SellerRequirement.BusinessAddress -> "Your business address"
    SellerRequirement.AddressProof -> "A proof of address (both sides of a voter ID or passport)"
    // Shown rather than dropped: a requirement this build does not recognise
    // is still one the server will refuse on, and a seller staring at an
    // apparently complete checklist that will not submit has no way forward.
    is SellerRequirement.Unknown -> key.replace('_', ' ').replaceFirstChar(Char::uppercase)
}

@Composable
fun SubmitShopScreen(
    onBack: () -> Unit,
    actions: SubmitShopActions,
    onSubmitted: () -> Unit,
    viewModel: SubmitShopViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    UsScaffold(topBar = { MSellerPageBar(title = "Submit for review", onBack = onBack) }) { padding ->
        when (val s = state) {
            is SubmitShopUiState.Loading -> UsLoadingState(
                modifier = Modifier.padding(padding),
                label = "Checking your shop",
            )

            is SubmitShopUiState.Failed -> UsErrorState(
                message = s.message,
                modifier = Modifier.padding(padding),
                onRetry = viewModel::refresh.takeIf { s.retryable },
            )

            is SubmitShopUiState.Content -> SubmitShopContent(
                state = s,
                modifier = Modifier
                    .padding(padding)
                    .verticalScroll(rememberScrollState()),
                actions = actions,
                onSubmit = { viewModel.submit(onSubmitted) },
            )
        }
    }
}

@Composable
private fun SubmitShopContent(
    state: SubmitShopUiState.Content,
    modifier: Modifier = Modifier,
    actions: SubmitShopActions,
    onSubmit: () -> Unit,
) {
    Column(
        modifier = modifier.padding(vertical = UsTheme.spacing.m),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        state.scope?.let { scope ->
            Text(
                text = "Selling: ${scope.label}",
                style = MaterialTheme.typography.labelLarge,
                color = UsTheme.extended.textSecondary,
            )
        }
        if (state.ready) {
            Text(
                text = "Everything is in place. Submitting sends your shop for review; " +
                    "we will tell you when it is approved.",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textPrimary,
            )
        } else {
            Text(
                text = "Still needed before we can review your shop:",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textPrimary,
            )
            // The WHOLE remaining list, not the first item. The server names
            // everything missing at once for the same reason: fixing one thing
            // and being refused again turns a five-minute task into five round
            // trips.
            for (requirement in state.missing) {
                MissingRow(requirement)
            }
        }

        // A shortcut for every gap the app can resolve ([submitShortcuts]).
        for (shortcut in submitShortcuts(state.missing)) {
            UsSecondaryButton(
                text = shortcut.label,
                onClick = { actions.open(shortcut) },
                modifier = Modifier.fillMaxWidth(),
            )
        }

        state.error?.let { error ->
            Text(
                text = error,
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.statusDanger,
            )
        }

        UsButton(
            text = "Submit for review",
            onClick = onSubmit,
            enabled = state.ready && !state.submitting,
            loading = state.submitting,
            modifier = Modifier.fillMaxWidth(),
        )
    }
}

@Composable
private fun MissingRow(requirement: SellerRequirement) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = "•",
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textSecondary,
        )
        Text(
            text = requirement.label(),
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textPrimary,
        )
    }
}
