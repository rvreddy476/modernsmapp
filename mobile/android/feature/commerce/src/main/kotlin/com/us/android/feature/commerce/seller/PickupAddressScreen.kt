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
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.commerce.ui.CommerceNotice

/**
 * The seller's pickup point.
 *
 * This is the origin of every shipment they send, and until it exists the
 * courier is quoted from whatever postcode happened to be on the seller row —
 * or from nowhere at all, if they skipped it during onboarding.
 *
 * State and PIN are required rather than optional, and both are labelled as
 * such, because both decide money: the PIN is where the courier collects and
 * what the delivery rate is quoted against, and the state is the seller half
 * of the GST place-of-supply comparison. A wrong state bills CGST+SGST on an
 * interstate sale.
 */
@Composable
fun PickupAddressScreen(
    onBack: () -> Unit,
    onSaved: () -> Unit,
    viewModel: PickupAddressViewModel = hiltViewModel(),
) {
    val form by viewModel.form.collectAsStateWithLifecycle()

    UsScaffold(topBar = { UsTopBar(title = "Pickup address", onBack = onBack) }) { padding ->
        Column(
            modifier = Modifier
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(vertical = UsTheme.spacing.m),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        ) {
            Text(
                text = "Where couriers collect your orders.",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textSecondary,
            )

            AddressFields(form = form, onChange = viewModel::update)

            if (form.saved) {
                CommerceNotice(text = "Saved. New shipments will be collected from here.")
            }

            form.error?.let { error ->
                Text(
                    text = error,
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.statusDanger,
                )
            }

            UsButton(
                text = "Save",
                onClick = { viewModel.save(onSaved) },
                enabled = form.isComplete && !form.saving,
                loading = form.saving,
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}

@Composable
private fun AddressFields(
    form: PickupAddressForm,
    onChange: ((PickupAddressForm) -> PickupAddressForm) -> Unit,
) {
    UsTextField(
        value = form.contactName,
        onValueChange = { v -> onChange { it.copy(contactName = v) } },
        label = "Contact name",
        enabled = !form.saving,
    )
    UsTextField(
        value = form.phone,
        onValueChange = { v -> onChange { it.copy(phone = v.filter(Char::isDigit)) } },
        label = "Phone",
        keyboardType = KeyboardType.Phone,
        enabled = !form.saving,
    )
    UsTextField(
        value = form.line1,
        onValueChange = { v -> onChange { it.copy(line1 = v) } },
        label = "Address",
        enabled = !form.saving,
    )
    UsTextField(
        value = form.line2,
        onValueChange = { v -> onChange { it.copy(line2 = v) } },
        label = "Area, landmark (optional)",
        enabled = !form.saving,
    )
    UsTextField(
        value = form.city,
        onValueChange = { v -> onChange { it.copy(city = v) } },
        label = "City",
        enabled = !form.saving,
    )
    UsTextField(
        value = form.state,
        onValueChange = { v -> onChange { it.copy(state = v) } },
        label = "State",
        // Said plainly rather than left to a validation error, because a
        // seller has no way to guess that this field is what decides which GST
        // is charged.
        placeholder = "Decides the GST charged on your sales",
        enabled = !form.saving,
    )
    UsTextField(
        value = form.postalCode,
        onValueChange = { v ->
            onChange { it.copy(postalCode = v.filter(Char::isDigit).take(PIN_DIGITS)) }
        },
        label = "PIN code",
        placeholder = "Where the courier collects",
        keyboardType = KeyboardType.Number,
        enabled = !form.saving,
    )
}

private const val PIN_DIGITS = 6
