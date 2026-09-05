package com.us.android.feature.commerce.address

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.commerce.model.Address
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.commerce.ui.CommerceNotice
import com.us.android.feature.commerce.ui.MStorePageBar
import com.us.android.feature.commerce.ui.pressScale

/** Renders an address as a single readable block. */
fun Address.summary(): String = listOfNotNull(
    contactName,
    line1,
    line2?.takeIf { it.isNotBlank() },
    landmark?.takeIf { it.isNotBlank() },
    "$city, $state $postalCode",
    phone,
).joinToString("\n")

/** A one-line form for compact contexts such as the checkout summary. */
fun Address.oneLine(): String =
    listOfNotNull(line1, city, postalCode).joinToString(", ")

/**
 * The address book, in two modes.
 *
 * Inside checkout ([onContinue] non-null) it is a PICKER: choosing an address
 * is what unlocks the delivery quote, so the continue button carries the
 * chosen id forward rather than the screen holding it. Opened from MStore's
 * profile menu ([onContinue] null) it is the address book itself — the same
 * list and the same "Add another", without a "Deliver here" that would have
 * nowhere to go.
 *
 * One screen rather than two, because the second copy is where the list, the
 * empty state and the add flow drift apart.
 */
@Composable
@Suppress("LongMethod")
fun AddressScreen(
    onBack: () -> Unit,
    onContinue: ((addressId: String, summary: String) -> Unit)?,
    onAddAddress: () -> Unit,
    modifier: Modifier = Modifier,
    viewModel: AddressViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val picking = onContinue != null

    UsScaffold(
        modifier = modifier,
        topBar = {
            MStorePageBar(
                title = if (picking) "Delivery address" else "Your addresses",
                onBack = onBack,
            )
        },
        applyPageGutter = false,
    ) { padding ->
        when (val s = state) {
            AddressUiState.Loading -> UsLoadingState(
                modifier = Modifier.padding(padding),
                label = "Loading addresses",
            )

            AddressUiState.Empty -> Column(
                modifier = Modifier
                    .padding(padding)
                    .padding(UsTheme.spacing.pageHorizontal),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            ) {
                Text(
                    text = "Add a delivery address",
                    style = MaterialTheme.typography.titleMedium,
                    color = UsTheme.extended.textPrimary,
                )
                Text(
                    text = "We need somewhere to send your order before we can " +
                        "work out delivery.",
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textSecondary,
                )
                UsButton(
                    text = "Add address",
                    onClick = onAddAddress,
                    modifier = Modifier.fillMaxWidth(),
                )
            }

            is AddressUiState.Failed -> UsErrorState(
                message = s.message,
                modifier = Modifier.padding(padding),
                onRetry = viewModel::refresh.takeIf { s.retryable },
            )

            is AddressUiState.Content -> Column(
                modifier = Modifier.padding(padding),
            ) {
                LazyColumn(
                    modifier = Modifier.weight(1f),
                    contentPadding = androidx.compose.foundation.layout.PaddingValues(
                        horizontal = UsTheme.spacing.pageHorizontal,
                        vertical = UsTheme.spacing.s,
                    ),
                    verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
                ) {
                    items(s.addresses, key = { it.id }) { address ->
                        AddressCard(
                            address = address,
                            selected = address.id == s.selectedId,
                            onClick = { viewModel.select(address.id) },
                        )
                    }
                    item {
                        UsSecondaryButton(
                            text = "Add another address",
                            onClick = onAddAddress,
                            modifier = Modifier.fillMaxWidth(),
                        )
                    }
                }
                // No "Deliver here" outside checkout: a primary control that
                // leads nowhere is worse than its absence.
                if (onContinue != null) {
                    Column(
                        modifier = Modifier
                            .fillMaxWidth()
                            .background(UsTheme.extended.bgCard)
                            .padding(UsTheme.spacing.pageHorizontal),
                    ) {
                        UsButton(
                            text = "Deliver here",
                            onClick = {
                                val chosen = s.addresses.firstOrNull { it.id == s.selectedId }
                                if (chosen != null) onContinue(chosen.id, chosen.oneLine())
                            },
                            enabled = s.selectedId != null,
                            modifier = Modifier.fillMaxWidth(),
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun AddressCard(address: Address, selected: Boolean, onClick: () -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            // Selected is WHITE, here and on every other picker in the shop.
            // The accent belongs to "Deliver here", the action this choice
            // leads to.
            .border(
                width = if (selected) SELECTED_BORDER else UNSELECTED_BORDER,
                color = if (selected) Color.White else UsTheme.extended.borderSubtle,
                shape = RoundedCornerShape(UsTheme.radii.medium),
            )
            .background(UsTheme.extended.bgCard)
            .pressScale(onClick = onClick, role = Role.RadioButton)
            .padding(UsTheme.spacing.l),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        Text(
            text = address.label,
            style = MaterialTheme.typography.labelLarge,
            color = UsTheme.extended.textPrimary,
        )
        Text(
            text = address.summary(),
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textSecondary,
        )
    }
}

/** The new-address form. */
@Composable
@Suppress("LongMethod")
fun AddAddressScreen(
    onBack: () -> Unit,
    onSaved: () -> Unit,
    modifier: Modifier = Modifier,
    viewModel: AddressViewModel = hiltViewModel(),
) {
    val form by viewModel.form.collectAsStateWithLifecycle()

    UsScaffold(
        modifier = modifier,
        topBar = { MStorePageBar(title = "Add address", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        Column(
            modifier = Modifier
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(horizontal = UsTheme.spacing.pageHorizontal),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        ) {
            form.error?.let { CommerceNotice(text = it) }

            UsTextField(
                value = form.label,
                onValueChange = { v -> viewModel.updateForm { it.copy(label = v) } },
                label = "Label (Home, Work)",
                modifier = Modifier.fillMaxWidth(),
            )
            UsTextField(
                value = form.contactName,
                onValueChange = { v -> viewModel.updateForm { it.copy(contactName = v) } },
                label = "Full name",
                modifier = Modifier.fillMaxWidth(),
            )
            UsTextField(
                value = form.phone,
                onValueChange = { v -> viewModel.updateForm { it.copy(phone = v) } },
                label = "Phone",
                modifier = Modifier.fillMaxWidth(),
            )
            UsTextField(
                value = form.line1,
                onValueChange = { v -> viewModel.updateForm { it.copy(line1 = v) } },
                label = "Address line 1",
                modifier = Modifier.fillMaxWidth(),
            )
            UsTextField(
                value = form.line2,
                onValueChange = { v -> viewModel.updateForm { it.copy(line2 = v) } },
                label = "Address line 2 (optional)",
                modifier = Modifier.fillMaxWidth(),
            )
            UsTextField(
                value = form.landmark,
                onValueChange = { v -> viewModel.updateForm { it.copy(landmark = v) } },
                label = "Landmark (optional)",
                modifier = Modifier.fillMaxWidth(),
            )
            UsTextField(
                value = form.city,
                onValueChange = { v -> viewModel.updateForm { it.copy(city = v) } },
                label = "City",
                modifier = Modifier.fillMaxWidth(),
            )
            UsTextField(
                value = form.state,
                onValueChange = { v -> viewModel.updateForm { it.copy(state = v) } },
                label = "State",
                modifier = Modifier.fillMaxWidth(),
            )
            UsTextField(
                value = form.postalCode,
                onValueChange = { v -> viewModel.updateForm { it.copy(postalCode = v) } },
                label = "PIN code",
                modifier = Modifier.fillMaxWidth(),
            )

            Text(
                text = "We check whether we can deliver to this PIN code when you " +
                    "continue to checkout.",
                style = MaterialTheme.typography.labelSmall,
                color = UsTheme.extended.textSecondary,
            )

            UsButton(
                text = "Save address",
                onClick = { viewModel.saveAddress { onSaved() } },
                enabled = form.isComplete && !form.saving,
                loading = form.saving,
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(bottom = UsTheme.spacing.xxl),
            )
        }
    }
}

private val SELECTED_BORDER = 2.dp
private val UNSELECTED_BORDER = 1.dp
