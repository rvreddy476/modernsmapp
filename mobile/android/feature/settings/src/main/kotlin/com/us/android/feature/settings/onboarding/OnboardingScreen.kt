package com.us.android.feature.settings.onboarding

import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.toggleable
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Checkbox
import androidx.compose.material3.CheckboxDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.profile.data.AppModule
import com.us.android.core.profile.data.ModulePreferences
import com.us.android.core.ui.UsSettingsRadioRow

/**
 * The module picker. One screen, two registrations: first-login onboarding
 * (no back arrow, "Continue") and the settings form ("Save", back arrow).
 *
 * The list is every module the SERVER accepts, not only the ones this build
 * has a screen for. A choice made today for a module that arrives next month
 * is honoured then; hiding it would mean asking again.
 */
@Composable
fun OnboardingScreen(
    title: String,
    actionLabel: String,
    onBack: (() -> Unit)?,
    onSaved: () -> Unit,
    viewModel: OnboardingViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val editing = state as? OnboardingUiState.Editing

    LaunchedEffect(editing?.saved) {
        if (editing?.saved == true) onSaved()
    }

    UsScaffold(
        topBar = { UsTopBar(title = title, onBack = onBack) },
        bottomBar = {
            if (editing != null) {
                UsButton(
                    text = actionLabel,
                    onClick = viewModel::save,
                    loading = editing.saving,
                    modifier = Modifier
                        .fillMaxWidth()
                        .navigationBarsPadding()
                        .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.xxl),
                )
            }
        },
        applyPageGutter = false,
    ) { padding ->
        if (editing == null) {
            Box(modifier = Modifier.fillMaxSize().padding(padding), contentAlignment = Alignment.Center) {
                CircularProgressIndicator(color = UsTheme.extended.chatAccent)
            }
        } else {
            OnboardingContent(
                prefs = editing.value,
                message = editing.message,
                onToggle = viewModel::toggleModule,
                onSelectHome = viewModel::selectHome,
                modifier = Modifier.padding(padding),
            )
        }
    }
}

@Composable
private fun OnboardingContent(
    prefs: ModulePreferences,
    message: String?,
    onToggle: (AppModule, Boolean) -> Unit,
    onSelectHome: (AppModule) -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(horizontal = UsTheme.spacing.pageHorizontal),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Spacer(modifier = Modifier.height(UsTheme.spacing.m))
        SectionHeading(
            title = "Pick what you use",
            subtitle = "Only the parts you switch on appear in the app. Change this any time in Settings.",
        )
        AppModule.selectable.forEach { module ->
            ModuleCard(
                module = module,
                checked = module in prefs.modules,
                onCheckedChange = { onToggle(module, it) },
            )
        }
        Spacer(modifier = Modifier.height(UsTheme.spacing.m))
        SectionHeading(
            title = "Choose your home page",
            subtitle = "The page that opens first.",
        )
        Column {
            prefs.homeCandidates.forEach { module ->
                UsSettingsRadioRow(
                    title = module.displayName,
                    selected = module == prefs.homeModule,
                    onClick = { onSelectHome(module) },
                )
            }
        }
        message?.let {
            Text(
                text = it,
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.error,
            )
        }
        Spacer(modifier = Modifier.height(UsTheme.spacing.xxl))
    }
}

@Composable
private fun SectionHeading(title: String, subtitle: String) {
    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
        Text(
            text = title,
            style = MaterialTheme.typography.titleLarge,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.semantics { heading() },
        )
        Text(
            text = subtitle,
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textMuted,
        )
    }
}

/** One module: name, a one-line description and a checkbox; the card is the target. */
@Composable
private fun ModuleCard(
    module: AppModule,
    checked: Boolean,
    onCheckedChange: (Boolean) -> Unit,
) {
    val shape = RoundedCornerShape(UsTheme.radii.large)
    val accent = UsTheme.extended.chatAccent
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clip(shape)
            .border(
                width = if (checked) SELECTED_BORDER else UNSELECTED_BORDER,
                color = if (checked) accent else UsTheme.extended.borderSubtle,
                shape = shape,
            )
            .toggleable(value = checked, role = Role.Checkbox, onValueChange = onCheckedChange)
            .padding(horizontal = UsTheme.spacing.xxl, vertical = UsTheme.spacing.xl),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Column(
            modifier = Modifier.weight(1f),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
        ) {
            Text(
                text = module.displayName,
                style = MaterialTheme.typography.titleMedium,
                color = UsTheme.extended.textPrimary,
            )
            Text(
                text = module.description,
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
        }
        Checkbox(
            checked = checked,
            // The card handles the tap; one target, not two.
            onCheckedChange = null,
            colors = CheckboxDefaults.colors(checkedColor = accent),
        )
    }
}

private val AppModule.description: String
    get() = when (this) {
        AppModule.FEED -> "Posts from people you follow"
        AppModule.REELS -> "Short vertical videos"
        AppModule.COMMERCE -> "Shop and sell"
        AppModule.CHAT -> "Messages, groups and calls"
        AppModule.DATING -> "Meet people"
        AppModule.FOOD -> "Recipes and places to eat"
        AppModule.QA -> "Ask and answer questions"
        AppModule.POSTTUBE -> "Long videos"
    }

private val SELECTED_BORDER = 2.dp
private val UNSELECTED_BORDER = 1.dp
