package com.us.android.feature.doorsteppro.onboarding

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.doorsteppro.data.BankRequest
import com.us.android.feature.doorsteppro.ui.UploadField
import com.us.android.feature.doorsteppro.ui.UploadUi

data class BankFormData(
    val holder: String = "", val account: String = "", val confirm: String = "", val ifsc: String = "",
    val bankName: String = "", val branchName: String = "", val proofKind: String = "cancelled_cheque",
    val proof: UploadUi = UploadUi.Idle,
) {
    fun errors(): Map<String, String> = buildMap {
        if (!Regex("[\\p{L} .'-]{2,100}").matches(holder.trim())) put("holder", "Enter the name on your bank account")
        if (!Regex("[0-9]{9,18}").matches(account)) put("account", "Enter a 9–18 digit account number")
        if (confirm != account) put("confirm", "Account numbers do not match")
        if (!Regex("[A-Z]{4}0[A-Z0-9]{6}").matches(ifsc.trim().uppercase())) put("ifsc", "Enter a valid 11-character IFSC")
        if (bankName.trim().length !in 2..100) put("bankName", "Enter your bank name")
        if (branchName.isNotBlank() && branchName.trim().length !in 2..100) put("branchName", "Enter a valid branch name")
        if (proofKind !in BANK_PROOFS.map { it.first }) put("proofKind", "Choose a proof type")
        if (proof !is UploadUi.Uploaded) put("proof", "Upload your bank proof")
    }
    fun request() = BankRequest(holder.trim(), account, ifsc.trim().uppercase(), bankName.trim(), branchName.trim(), proofKind, (proof as UploadUi.Uploaded).mediaId)
    override fun toString() = "BankFormData(redacted)"
}

val BANK_PROOFS = listOf("cancelled_cheque" to "Cancelled cheque", "bank_statement" to "Bank statement", "passbook" to "Passbook")

@Composable
fun ChoiceField(label: String, value: String, options: List<Pair<String, String>>, onChange: (String) -> Unit) {
    var expanded by remember { mutableStateOf(false) }
    Column {
        UsPillButton(text = "$label: ${options.find { it.first == value }?.second ?: "Select"}", onClick = { expanded = true }, filled = false)
        DropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
            options.forEach { (key, name) -> DropdownMenuItem(text = { Text(name) }, onClick = { onChange(key); expanded = false }) }
        }
    }
}

@Composable
fun BankFields(value: BankFormData, errors: Map<String, String>, onChange: (BankFormData) -> Unit, onPickProof: () -> Unit) {
    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
        UsTextField(value.holder, { onChange(value.copy(holder = it.take(100))) }, label = "Account holder name *", errorText = errors["holder"])
        UsTextField(value.account, { onChange(value.copy(account = it.filter { c -> c in '0'..'9' }.take(18))) }, label = "Account number *", isPassword = true, keyboardType = KeyboardType.NumberPassword, errorText = errors["account"])
        UsTextField(value.confirm, { onChange(value.copy(confirm = it.filter { c -> c in '0'..'9' }.take(18))) }, label = "Confirm account number *", isPassword = true, keyboardType = KeyboardType.NumberPassword, errorText = errors["confirm"])
        UsTextField(value.ifsc, { onChange(value.copy(ifsc = it.uppercase().take(11))) }, label = "IFSC *", errorText = errors["ifsc"])
        UsTextField(value.bankName, { onChange(value.copy(bankName = it.take(100))) }, label = "Bank name *", errorText = errors["bankName"])
        UsTextField(value.branchName, { onChange(value.copy(branchName = it.take(100))) }, label = "Branch (optional)", errorText = errors["branchName"])
        ChoiceField("Proof type *", value.proofKind, BANK_PROOFS) { onChange(value.copy(proofKind = it, proof = UploadUi.Idle)) }
        errors["proofKind"]?.let { Text(it, color = androidx.compose.material3.MaterialTheme.colorScheme.error) }
        UploadField("Bank proof *", value.proof, onPickProof, hint = "Image up to 10 MB · name, account number and IFSC")
        errors["proof"]?.let { Text(it, color = androidx.compose.material3.MaterialTheme.colorScheme.error) }
    }
}
