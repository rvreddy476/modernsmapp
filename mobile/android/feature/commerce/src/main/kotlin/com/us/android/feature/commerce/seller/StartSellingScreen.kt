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
import androidx.compose.ui.text.input.KeyboardType
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.commerce.repository.CommerceResult
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.commerce.ui.CommerceNotice
import com.us.android.feature.commerce.ui.MSellerPageBar
import com.us.android.feature.commerce.ui.describe
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

data class StartSellingForm(
    val storeName: String = "",
    val email: String = "",
    val saving: Boolean = false,
    val error: String? = null,
) {
    val isComplete: Boolean
        get() = storeName.trim().length >= MIN_STORE_NAME &&
            email.contains('@') &&
            email.substringAfterLast('@').contains('.')
}

private const val MIN_STORE_NAME = 3

@HiltViewModel
class StartSellingViewModel @Inject constructor(
    private val repo: CommerceRepository,
) : ViewModel() {

    private val _form = MutableStateFlow(StartSellingForm())
    val form: StateFlow<StartSellingForm> = _form.asStateFlow()

    fun update(transform: (StartSellingForm) -> StartSellingForm) {
        _form.value = transform(_form.value).copy(error = null)
    }

    fun submit(onOpened: () -> Unit) {
        val form = _form.value
        if (!form.isComplete || form.saving) return

        _form.value = form.copy(saving = true, error = null)
        viewModelScope.launch {
            when (val r = repo.startSelling(form.storeName, form.email)) {
                is CommerceResult.Failure ->
                    _form.value = form.copy(saving = false, error = r.error.describe())

                is CommerceResult.Success -> {
                    _form.value = form.copy(saving = false)
                    onOpened()
                }
            }
        }
    }
}

/**
 * Opening a shop.
 *
 * Two fields, deliberately. The server's onboarding wizard has seven steps —
 * basic details, storefront, KYC documents, fulfilment, payout — and asking
 * for all of them before anything exists means a seller who abandons halfway
 * has nothing to come back to. `POST /onboarding/start` creates a DRAFT from a
 * name and an email alone, and every later step edits that draft.
 *
 * The call is idempotent on the server, which is what makes a double tap safe:
 * a user who already has a draft gets it back rather than a second shop.
 *
 * This screen does not submit the application. A draft is not a claim to sell
 * anything, and the seller hub shows the remaining steps once the shop exists.
 */
@Composable
fun StartSellingScreen(
    onBack: () -> Unit,
    onOpened: () -> Unit,
    viewModel: StartSellingViewModel = hiltViewModel(),
) {
    val form by viewModel.form.collectAsStateWithLifecycle()

    UsScaffold(topBar = { MSellerPageBar(title = "Start selling", onBack = onBack) }) { padding ->
        Column(
            modifier = Modifier
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(vertical = UsTheme.spacing.m),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        ) {
            Text(
                text = "Give your shop a name to get started. You can add your " +
                    "products, pickup address and payout details afterwards.",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textSecondary,
            )

            UsTextField(
                value = form.storeName,
                onValueChange = { v -> viewModel.update { it.copy(storeName = v) } },
                label = "Shop name",
                placeholder = "What buyers will see",
                enabled = !form.saving,
            )
            UsTextField(
                value = form.email,
                onValueChange = { v -> viewModel.update { it.copy(email = v.trim()) } },
                label = "Contact email",
                placeholder = "Where we send order and payout notices",
                keyboardType = KeyboardType.Email,
                enabled = !form.saving,
            )

            // Said up front. A seller who lists ten products and only then
            // learns none of them are visible has spent an evening on
            // something they were never told about.
            CommerceNotice(
                text = "Your shop is reviewed before anything goes on sale. You can " +
                    "add products while you wait.",
            )

            form.error?.let { error ->
                Text(
                    text = error,
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.statusDanger,
                )
            }

            UsButton(
                text = "Open my shop",
                onClick = { viewModel.submit(onOpened) },
                enabled = form.isComplete && !form.saving,
                loading = form.saving,
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}
