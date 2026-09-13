package com.us.android.feature.kitchen.payout

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.kitchen.kycui.BankAccountForm
import com.us.android.feature.kitchen.kycui.SavedBankAccountCard
import com.us.android.feature.kitchen.ui.InfoNote
import com.us.android.feature.kitchen.ui.KitchenScreen
import com.us.android.feature.kitchen.ui.LoadingPane

@Composable
fun PayoutScreen(onBack: () -> Unit, viewModel: PayoutViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    KitchenScreen(
        title = "Bank account",
        onBack = onBack,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
    ) { padding ->
        if (state.loading) {
            LoadingPane(Modifier.padding(padding))
            return@KitchenScreen
        }
        Column(
            modifier = Modifier
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(top = padding.calculateTopPadding() + 8.dp, bottom = padding.calculateBottomPadding() + 96.dp),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            val existing = state.existing
            if (existing != null && !state.showForm) {
                SavedBankAccountCard(
                    holderName = existing.holderName,
                    maskedAccountNumber = existing.accountNumberMasked,
                    ifsc = existing.ifsc,
                    verificationStatus = existing.verificationStatus,
                    onReplace = viewModel::startReplacing,
                )
                InfoNote("Feast pays your earnings into this account. Only the last four digits are ever shown.")
            } else {
                BankAccountForm(
                    state = state.form,
                    errors = state.errors,
                    onHolderNameChange = viewModel::onHolderName,
                    onAccountNumberChange = viewModel::onAccountNumber,
                    onConfirmAccountNumberChange = viewModel::onConfirmAccountNumber,
                    onIfscChange = viewModel::onIfsc,
                    enabled = !state.saving,
                )
                InfoNote("The account number is encrypted when it is saved, and only its last four digits are shown again.")
                UsButton(
                    text = "Save bank account",
                    onClick = viewModel::save,
                    loading = state.saving,
                    modifier = Modifier.fillMaxWidth(),
                )
                if (state.replacing) {
                    UsSecondaryButton(
                        text = "Keep the current account",
                        onClick = viewModel::cancelReplacing,
                        modifier = Modifier.fillMaxWidth(),
                    )
                }
            }
        }
    }
}
