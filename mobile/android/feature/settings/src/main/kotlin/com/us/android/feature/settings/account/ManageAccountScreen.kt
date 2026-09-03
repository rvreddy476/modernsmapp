package com.us.android.feature.settings.account

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.profile.data.AccountSummary
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.core.ui.UsSettingsLinkRow
import com.us.android.core.ui.UsSettingsSection
import com.us.android.core.ui.UsSettingsSelectRow

@Composable
fun ManageAccountScreen(
    onBack: () -> Unit,
    onAccountControl: () -> Unit,
    viewModel: ManageAccountViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    UsScaffold(
        topBar = { UsTopBar("Manage account", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        when (val current = state) {
            ManageAccountUiState.Loading -> UsLoadingState(Modifier.padding(padding), "Loading account")
            is ManageAccountUiState.Error ->
                UsErrorState(current.message, Modifier.padding(padding), onRetry = viewModel::load)
            is ManageAccountUiState.Loaded ->
                ManageAccountContent(current, viewModel, onAccountControl, Modifier.padding(padding))
        }
    }
}

@Composable
private fun ManageAccountContent(
    state: ManageAccountUiState.Loaded,
    viewModel: ManageAccountViewModel,
    onAccountControl: () -> Unit,
    modifier: Modifier,
) {
    val account = state.account
    Column(
        modifier = modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(horizontal = UsTheme.spacing.pageHorizontal),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xl),
    ) {
        UsSettingsSection("Account information") {
            UsSettingsLinkRow(
                title = "Email",
                onClick = {},
                enabled = false,
                value = account.email.ifBlank { "Not set" },
                description = if (account.emailVerified) "Verified" else "Not verified",
            )
            UsSettingsLinkRow(
                title = "Phone",
                onClick = {},
                enabled = false,
                value = account.phone.ifBlank { "Not set" },
                description = if (account.phone.isBlank()) {
                    null
                } else if (account.phoneVerified) {
                    "Verified"
                } else {
                    "Not verified"
                },
            )
            UsSettingsLinkRow(
                title = "Account type",
                onClick = {},
                enabled = false,
                value = account.accountType.ifBlank { "Standard" },
            )
            UsSettingsSelectRow(
                title = "Region",
                selected = state.region,
                options = Countries.all,
                onSelected = viewModel::setRegion,
                enabled = !state.savingRegion,
            )
        }
        state.message?.let {
            Text(it, color = MaterialTheme.colorScheme.error)
        }
        UsSettingsSection("Account control") {
            UsSettingsLinkRow(
                title = "Account control",
                onClick = onAccountControl,
                description = "Deactivate or permanently delete your account",
            )
        }
        AccountStatusNote(account)
    }
}

@Composable
private fun AccountStatusNote(account: AccountSummary) {
    val deactivatedAt = account.deactivatedAt
    val purgeDate = account.scheduledPurgeDate
    when {
        !purgeDate.isNullOrBlank() -> Text(
            "Your account is scheduled for deletion on $purgeDate. Log in again before then to cancel.",
            color = MaterialTheme.colorScheme.error,
        )
        !deactivatedAt.isNullOrBlank() -> Text(
            "Your account is deactivated. Log in again to reactivate it.",
            color = UsTheme.extended.textMuted,
        )
        else -> Unit
    }
}
