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
 * Where the seller is paid.
 *
 * The step never worked. `SaveOnboardingPayout` upserts on `(seller_id) WHERE
 * is_primary` and no index matched that specification, so every call — for
 * every seller, since the statement was written — failed with *"there is no
 * unique or exclusion constraint matching the ON CONFLICT specification"*.
 * Migration 021 adds the index; this is the screen that finally calls it.
 *
 * Either a bank account or a UPI id is enough. Small sellers often have only
 * one, and demanding the other is how a shop stalls at the last step.
 */
@Composable
fun PayoutScreen(
    onBack: () -> Unit,
    onSaved: () -> Unit,
    viewModel: PayoutViewModel = hiltViewModel(),
) {
    val form by viewModel.form.collectAsStateWithLifecycle()

    UsScaffold(topBar = { UsTopBar(title = "Get paid", onBack = onBack) }) { padding ->
        Column(
            modifier = Modifier
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(vertical = UsTheme.spacing.m),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        ) {
            Text(
                text = "Where your sales are settled. A bank account or a UPI id — " +
                    "either is enough.",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textSecondary,
            )

            UsTextField(
                value = form.accountHolderName,
                onValueChange = { v -> viewModel.update { it.copy(accountHolderName = v) } },
                label = "Account holder name",
                // Said plainly: a mismatch here is the commonest reason a
                // payout bounces, and it bounces days later.
                placeholder = "Exactly as your bank has it",
                enabled = !form.saving,
            )

            PayoutFields(form = form, onChange = viewModel::update)

            if (form.saved) {
                CommerceNotice(text = "Saved. This is where your sales will be settled.")
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
private fun PayoutFields(
    form: PayoutForm,
    onChange: ((PayoutForm) -> PayoutForm) -> Unit,
) {
    Text(
        text = "Bank account",
        style = MaterialTheme.typography.titleSmall,
        color = UsTheme.extended.textPrimary,
    )
    UsTextField(
        value = form.accountNumber,
        onValueChange = { v ->
            onChange { it.copy(accountNumber = v.filter(Char::isDigit).take(MAX_ACCOUNT_DIGITS)) }
        },
        label = "Account number",
        keyboardType = KeyboardType.Number,
        enabled = !form.saving,
    )
    UsTextField(
        value = form.ifscCode,
        onValueChange = { v ->
            onChange { it.copy(ifscCode = v.filter(Char::isLetterOrDigit).uppercase().take(IFSC_CHARS)) }
        },
        label = "IFSC code",
        // A half-filled bank section is a seller who started and stopped, not
        // a seller who chose UPI. Saying so beats silently ignoring the field.
        errorText = "An account number needs its IFSC code too"
            .takeIf { form.bankPartiallyFilled },
        enabled = !form.saving,
    )
    UsTextField(
        value = form.bankName,
        onValueChange = { v -> onChange { it.copy(bankName = v) } },
        label = "Bank name (optional)",
        enabled = !form.saving,
    )

    Text(
        text = "Or UPI",
        style = MaterialTheme.typography.titleSmall,
        color = UsTheme.extended.textPrimary,
    )
    UsTextField(
        value = form.upiId,
        onValueChange = { v -> onChange { it.copy(upiId = v.trim()) } },
        label = "UPI id",
        placeholder = "yourname@bank",
        enabled = !form.saving,
    )
}

private const val MAX_ACCOUNT_DIGITS = 18
private const val IFSC_CHARS = 11
