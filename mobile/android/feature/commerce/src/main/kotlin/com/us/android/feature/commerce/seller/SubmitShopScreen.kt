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
import com.us.android.core.commerce.model.SellerRequirement
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.commerce.repository.CommerceResult
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
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

/** What each outstanding requirement means, in the seller's terms. */
fun SellerRequirement.label(): String = when (this) {
    SellerRequirement.StoreName -> "A name for your shop"
    SellerRequirement.Email -> "A contact email"
    SellerRequirement.PickupAddress -> "A pickup address"
    SellerRequirement.PayoutAccount -> "Where you get paid"
    SellerRequirement.KycDocument -> "An identity document"
    // Shown rather than dropped: a requirement this build does not recognise
    // is still one the server will refuse on, and a seller staring at an
    // apparently complete checklist that will not submit has no way forward.
    is SellerRequirement.Unknown -> key.replace('_', ' ').replaceFirstChar(Char::uppercase)
}

@Composable
fun SubmitShopScreen(
    onBack: () -> Unit,
    onOpenPickupAddress: () -> Unit,
    onOpenPayout: () -> Unit,
    onOpenDocument: () -> Unit,
    onSubmitted: () -> Unit,
    viewModel: SubmitShopViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    UsScaffold(topBar = { UsTopBar(title = "Submit for review", onBack = onBack) }) { padding ->
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
                onOpenPickupAddress = onOpenPickupAddress,
                onOpenPayout = onOpenPayout,
                onOpenDocument = onOpenDocument,
                onSubmit = { viewModel.submit(onSubmitted) },
            )
        }
    }
}

@Composable
private fun SubmitShopContent(
    state: SubmitShopUiState.Content,
    modifier: Modifier = Modifier,
    onOpenPickupAddress: () -> Unit,
    onOpenPayout: () -> Unit,
    onOpenDocument: () -> Unit,
    onSubmit: () -> Unit,
) {
    Column(
        modifier = modifier.padding(vertical = UsTheme.spacing.m),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
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

        // Shortcuts to the two the app can actually resolve. The identity
        // document needs a file upload, which is not built — so it is listed
        // and not linked, rather than offering a button that goes nowhere.
        if (SellerRequirement.PickupAddress in state.missing) {
            UsSecondaryButton(
                text = "Add a pickup address",
                onClick = onOpenPickupAddress,
                modifier = Modifier.fillMaxWidth(),
            )
        }
        if (SellerRequirement.PayoutAccount in state.missing) {
            UsSecondaryButton(
                text = "Add where you get paid",
                onClick = onOpenPayout,
                modifier = Modifier.fillMaxWidth(),
            )
        }
        if (SellerRequirement.KycDocument in state.missing) {
            UsSecondaryButton(
                text = "Send an identity document",
                onClick = onOpenDocument,
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
