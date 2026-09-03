package com.us.android.feature.settings.account

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme

/**
 * Two TikTok-style cards, each opening a password-confirmation sheet before
 * anything happens. Neither destructive action can be reached with a single
 * tap — that is the entire point of the sheet.
 */
@Composable
fun AccountControlScreen(
    onBack: () -> Unit,
    onSignedOut: () -> Unit,
    viewModel: AccountControlViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    LaunchedEffect(state.signedOut) {
        if (state.signedOut) onSignedOut()
    }

    UsScaffold(
        topBar = { UsTopBar("Account control", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        Column(
            modifier = Modifier
                .padding(padding)
                .fillMaxSize()
                .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.xl),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            AccountControlCard(
                title = "Deactivate account",
                body = "No one can see your account while it's deactivated. " +
                    "Reactivate any time by logging in.",
                actionLabel = "Deactivate",
                onClick = { viewModel.openSheet(AccountControlAction.DEACTIVATE) },
            )
            AccountControlCard(
                title = "Delete account permanently",
                body = "Your account and content will be deleted after 30 days. " +
                    "Log in within 30 days to cancel.",
                actionLabel = "Delete account",
                onClick = { viewModel.openSheet(AccountControlAction.DELETE) },
            )
        }
    }

    state.activeSheet?.let { action ->
        ConfirmSheet(action, state, viewModel)
    }
}

@Composable
private fun AccountControlCard(
    title: String,
    body: String,
    actionLabel: String,
    onClick: () -> Unit,
) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .background(UsTheme.extended.bgCard, RoundedCornerShape(UsTheme.radii.large))
            .padding(UsTheme.spacing.l),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        Text(title, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
        Text(body, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textMuted)
        UsSecondaryButton(text = actionLabel, onClick = onClick, modifier = Modifier.fillMaxWidth())
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun ConfirmSheet(
    action: AccountControlAction,
    state: AccountControlUiState,
    viewModel: AccountControlViewModel,
) {
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    val title = when (action) {
        AccountControlAction.DEACTIVATE -> "Confirm deactivation"
        AccountControlAction.DELETE -> "Confirm deletion"
    }
    val actionLabel = when (action) {
        AccountControlAction.DEACTIVATE -> "Deactivate"
        AccountControlAction.DELETE -> "Delete account"
    }
    ModalBottomSheet(onDismissRequest = viewModel::dismissSheet, sheetState = sheetState) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            Text(title, style = MaterialTheme.typography.titleLarge)
            Text(
                "Enter your password to continue.",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textMuted,
            )
            UsTextField(
                value = state.password,
                onValueChange = viewModel::setPassword,
                label = "Password",
                isPassword = true,
                enabled = !state.submitting,
                errorText = state.error,
            )
            UsButton(
                text = actionLabel,
                onClick = viewModel::confirm,
                modifier = Modifier.fillMaxWidth(),
                enabled = state.password.isNotBlank(),
                loading = state.submitting,
            )
        }
    }
}
