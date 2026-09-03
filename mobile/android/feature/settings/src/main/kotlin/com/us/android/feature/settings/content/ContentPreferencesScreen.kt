package com.us.android.feature.settings.content

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.core.ui.UsSettingsSection

@Composable
fun ContentPreferencesScreen(
    onBack: () -> Unit,
    viewModel: ContentPreferencesViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    UsScaffold(
        topBar = { UsTopBar("Content preferences", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        when (val current = state) {
            ContentPreferencesUiState.Loading -> UsLoadingState(Modifier.padding(padding), "Loading")
            is ContentPreferencesUiState.Error ->
                UsErrorState(current.message, Modifier.padding(padding), onRetry = viewModel::load)
            is ContentPreferencesUiState.Editing ->
                ContentPreferencesForm(current, viewModel, Modifier.padding(padding))
        }
    }
}

@Composable
private fun ContentPreferencesForm(
    state: ContentPreferencesUiState.Editing,
    viewModel: ContentPreferencesViewModel,
    modifier: Modifier,
) {
    Column(
        modifier = modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(horizontal = UsTheme.spacing.pageHorizontal),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        UsSettingsSection("Filter keywords") {
            Text(
                "Posts and comments containing these words are hidden from your feed.",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
        }
        UsTextField(
            value = state.draft,
            onValueChange = viewModel::setDraft,
            label = "Add keyword",
            errorText = state.draftError,
            enabled = !state.saving,
        )
        UsSecondaryButton(
            text = "Add keyword",
            onClick = viewModel::addKeyword,
            modifier = Modifier.fillMaxWidth(),
            enabled = !state.saving && state.draft.isNotBlank(),
        )
        state.message?.let { Text(it, color = MaterialTheme.colorScheme.error) }
        if (state.keywords.isEmpty()) {
            Text(
                "No keywords filtered yet.",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textMuted,
            )
        } else {
            Column {
                state.keywords.forEach { keyword ->
                    KeywordRow(keyword, enabled = !state.saving, onRemove = { viewModel.removeKeyword(keyword) })
                }
            }
        }
    }
}

@Composable
private fun KeywordRow(keyword: String, enabled: Boolean, onRemove: () -> Unit) {
    Column {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(vertical = UsTheme.spacing.m),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(keyword, style = MaterialTheme.typography.bodyLarge, color = UsTheme.extended.textPrimary)
            IconButton(onClick = onRemove, enabled = enabled) {
                Text("×", style = MaterialTheme.typography.headlineSmall, color = UsTheme.extended.textMuted)
            }
        }
        HorizontalDivider(color = UsTheme.extended.borderSubtle)
    }
}
