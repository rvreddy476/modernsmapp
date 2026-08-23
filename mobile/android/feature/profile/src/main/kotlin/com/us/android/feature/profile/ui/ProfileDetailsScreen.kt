package com.us.android.feature.profile.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Checkbox
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.profile.data.ProfileAboutItem
import com.us.android.core.profile.data.ProfileLink
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.core.ui.UsSettingsLinkRow
import com.us.android.core.ui.UsSettingsOption
import com.us.android.core.ui.UsSettingsSection
import com.us.android.core.ui.UsSettingsSelectRow

@Composable
fun ProfileDetailsScreen(onBack: () -> Unit, viewModel: ProfileDetailsViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    UsScaffold(topBar = { UsTopBar("About and links", onBack = onBack) }, applyPageGutter = false) { padding ->
        when {
            state.loading -> UsLoadingState(Modifier.padding(padding), "Loading profile details")
            state.error != null && state.about.isEmpty() && state.links.isEmpty() ->
                UsErrorState(state.error.orEmpty(), Modifier.padding(padding), onRetry = viewModel::load)
            else -> DetailsContent(state, viewModel, Modifier.padding(padding))
        }
    }
}

@Composable
private fun DetailsContent(state: ProfileDetailsUiState, vm: ProfileDetailsViewModel, modifier: Modifier) {
    var handle by remember(state.username) { mutableStateOf(state.username) }
    Column(
        modifier = modifier.fillMaxSize().verticalScroll(
            rememberScrollState()
        ).padding(horizontal = UsTheme.spacing.pageHorizontal),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xl),
    ) {
        UsSettingsSection("Handle") {
            UsTextField(handle, { handle = it.lowercase() }, "Username", placeholder = "your_handle")
            UsButton(
                "Change handle",
                { vm.changeHandle(handle) },
                Modifier.fillMaxWidth(),
                enabled = handle != state.username && !state.busy
            )
            Text(
                "Handle changes are protected by a server cooldown and old-handle reservation.",
                style = MaterialTheme.typography.bodySmall
            )
        }
        UsSettingsSection("About you") {
            state.about.forEach { item ->
                UsSettingsLinkRow(
                    item.title.ifBlank { item.section },
                    { vm.editAbout(item) },
                    description = listOf(item.subtitle, item.detail).filter(String::isNotBlank).joinToString(" · "),
                    value = if (item.visibility == "public") "Public" else "Only me"
                )
            }
            UsSecondaryButton("Add education, work or interest", { vm.editAbout(null) }, Modifier.fillMaxWidth())
            state.aboutDraft?.let { AboutEditor(it, state.busy, vm) }
        }
        UsSettingsSection("Profile links") {
            state.links.forEach { link ->
                UsSettingsLinkRow(
                    link.title,
                    { vm.editLink(link) },
                    description = link.url,
                    value = if (link.visibility == "public") "Public" else "Only me"
                )
            }
            UsSecondaryButton("Add link", { vm.editLink(null) }, Modifier.fillMaxWidth())
            state.linkDraft?.let { LinkEditor(it, state.busy, vm) }
        }
        state.error?.let { Text(it, color = MaterialTheme.colorScheme.primary) }
    }
}

@Composable
private fun AboutEditor(value: ProfileAboutItem, busy: Boolean, vm: ProfileDetailsViewModel) {
    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
        UsSettingsSelectRow("Section", value.section, ABOUT_SECTIONS, { selected ->
            vm.updateAbout { it.copy(section = selected) }
        })
        UsTextField(
            value.title,
            { text -> vm.updateAbout { it.copy(title = text) } },
            "Title",
            placeholder = "University, company or interest"
        )
        UsTextField(
            value.subtitle,
            { text -> vm.updateAbout { it.copy(subtitle = text) } },
            "Subtitle",
            placeholder = "Degree, role or level"
        )
        UsTextField(
            value.detail,
            { text -> vm.updateAbout { it.copy(detail = text) } },
            "Details",
            placeholder = "Dates or description",
            singleLine = false
        )
        UsSettingsSelectRow(
            "Visibility",
            value.visibility,
            VISIBILITY,
            { selected -> vm.updateAbout { it.copy(visibility = selected) } }
        )
        Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
            UsButton("Save", vm::saveAbout, Modifier.weight(1f), enabled = value.title.isNotBlank(), loading = busy)
            if (value.itemId.isNotBlank()) {
                UsSecondaryButton(
                    "Delete",
                    { vm.deleteAbout(value) },
                    Modifier.weight(1f),
                    enabled = !busy
                )
            }
            UsSecondaryButton("Cancel", vm::dismissEditor, Modifier.weight(1f), enabled = !busy)
        }
    }
}

@Composable
private fun LinkEditor(value: ProfileLink, busy: Boolean, vm: ProfileDetailsViewModel) {
    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
        UsTextField(
            value.title,
            { text -> vm.updateLink { it.copy(title = text) } },
            "Label",
            placeholder = "My website"
        )
        UsTextField(
            value.url,
            { text -> vm.updateLink { it.copy(url = text) } },
            "URL",
            placeholder = "https://example.com"
        )
        UsTextField(
            value.category,
            { text -> vm.updateLink { it.copy(category = text) } },
            "Category",
            placeholder = "portfolio"
        )
        UsSettingsSelectRow(
            "Visibility",
            value.visibility,
            VISIBILITY,
            { selected -> vm.updateLink { it.copy(visibility = selected) } }
        )
        Row(verticalAlignment = Alignment.CenterVertically) {
            Checkbox(value.pinned, { checked -> vm.updateLink { it.copy(pinned = checked) } })
            Text("Pin this link")
        }
        Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
            UsButton(
                "Save",
                vm::saveLink,
                Modifier.weight(1f),
                enabled = value.title.isNotBlank() && value.url.isNotBlank(),
                loading = busy
            )
            if (value.id.isNotBlank()) {
                UsSecondaryButton(
                    "Delete",
                    { vm.deleteLink(value) },
                    Modifier.weight(1f),
                    enabled = !busy
                )
            }
            UsSecondaryButton("Cancel", vm::dismissEditor, Modifier.weight(1f), enabled = !busy)
        }
    }
}

private val ABOUT_SECTIONS = listOf(
    "education",
    "work",
    "hobbies",
    "interests",
    "skills",
    "languages",
    "places",
    "achievements"
)
    .map { UsSettingsOption(it, it.replaceFirstChar(Char::uppercase)) }
private val VISIBILITY = listOf(UsSettingsOption("public", "Public"), UsSettingsOption("private", "Only me"))
