package com.us.android.feature.kitchen.kycui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.kitchen.kyc.BankAccountNumber
import com.us.android.feature.kitchen.kyc.Ifsc
import com.us.android.feature.kitchen.ui.CardHeading
import com.us.android.feature.kitchen.ui.KitchenCard
import com.us.android.feature.kitchen.ui.KitchenPill
import com.us.android.feature.kitchen.ui.LabeledValue
import com.us.android.feature.kitchen.ui.PillTone

/*
 * LIFTABLE into :core:kyc-ui. The form is stateless — values and errors in,
 * edits out — and its rules are pure, so Feast Rider's payout step can reuse
 * both once they move. The only kitchen imports are the small card/pill kit,
 * which moves with it.
 */

data class BankAccountFormState(
    val holderName: String = "",
    val accountNumber: String = "",
    val confirmAccountNumber: String = "",
    val ifsc: String = "",
)

enum class BankField { HOLDER, ACCOUNT, CONFIRM, IFSC }

/** Client checks mirroring shared/kyc; the server re-validates and penny-drops. */
object BankAccountFormRules {
    fun validate(form: BankAccountFormState): Map<BankField, String> = buildMap {
        if (form.holderName.isBlank()) put(BankField.HOLDER, "Enter the name on the bank account")
        val account = BankAccountNumber.normalize(form.accountNumber)
        when {
            account == null -> put(BankField.ACCOUNT, BankAccountNumber.INVALID_MESSAGE)
            form.confirmAccountNumber.trim() != account -> put(BankField.CONFIRM, "The account numbers don't match")
        }
        if (Ifsc.normalize(form.ifsc) == null) put(BankField.IFSC, Ifsc.INVALID_MESSAGE)
    }

    /** The server's request field for a 422, mapped back onto the form. */
    fun fieldFor(serverField: String?): BankField? = when (serverField) {
        "holder_name" -> BankField.HOLDER
        "account_number" -> BankField.ACCOUNT
        "ifsc" -> BankField.IFSC
        else -> null
    }
}

@Composable
fun BankAccountForm(
    state: BankAccountFormState,
    errors: Map<BankField, String>,
    onHolderNameChange: (String) -> Unit,
    onAccountNumberChange: (String) -> Unit,
    onConfirmAccountNumberChange: (String) -> Unit,
    onIfscChange: (String) -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
) {
    Column(modifier = modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
        UsTextField(
            value = state.holderName,
            onValueChange = onHolderNameChange,
            label = "Account holder name",
            errorText = errors[BankField.HOLDER],
            enabled = enabled,
        )
        UsTextField(
            value = state.accountNumber,
            onValueChange = { onAccountNumberChange(it.filter(Char::isDigit).take(MAX_ACCOUNT_DIGITS)) },
            label = "Account number",
            errorText = errors[BankField.ACCOUNT],
            isPassword = true,
            keyboardType = KeyboardType.NumberPassword,
            enabled = enabled,
        )
        UsTextField(
            value = state.confirmAccountNumber,
            onValueChange = { onConfirmAccountNumberChange(it.filter(Char::isDigit).take(MAX_ACCOUNT_DIGITS)) },
            label = "Re-enter account number",
            errorText = errors[BankField.CONFIRM],
            keyboardType = KeyboardType.Number,
            enabled = enabled,
        )
        UsTextField(
            value = state.ifsc,
            onValueChange = { onIfscChange(it.uppercase().take(IFSC_LENGTH)) },
            label = "IFSC",
            placeholder = "HDFC0001234",
            errorText = errors[BankField.IFSC],
            enabled = enabled,
        )
    }
}

/** What is shown after a save: masked only, never the number that was typed. */
@Composable
fun SavedBankAccountCard(
    holderName: String,
    maskedAccountNumber: String,
    ifsc: String,
    verificationStatus: String,
    onReplace: () -> Unit,
    modifier: Modifier = Modifier,
) {
    KitchenCard(modifier = modifier) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            CardHeading(title = "Bank account", modifier = Modifier.weight(1f))
            val (label, tone) = when (verificationStatus) {
                "VERIFIED" -> "Verified" to PillTone.Positive
                "FAILED", "REJECTED" -> "Verification failed" to PillTone.Danger
                else -> "Not verified yet" to PillTone.Warning
            }
            KitchenPill(label, tone)
        }
        LabeledValue("Holder", holderName)
        LabeledValue("Account", maskedAccountNumber)
        LabeledValue("IFSC", ifsc)
        UsPillButton(text = "Replace account", onClick = onReplace, filled = false)
    }
}

private const val MAX_ACCOUNT_DIGITS = 18
private const val IFSC_LENGTH = 11
