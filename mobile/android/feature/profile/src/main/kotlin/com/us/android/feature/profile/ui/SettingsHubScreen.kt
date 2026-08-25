package com.us.android.feature.profile.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsSettingsLinkRow
import com.us.android.core.ui.UsSettingsSection

@Composable
fun SettingsHubScreen(
    onBack: () -> Unit,
    onEditProfile: () -> Unit,
    onProfileDetails: () -> Unit,
    onPrivacy: () -> Unit,
    onNotifications: () -> Unit,
    onSecurity: () -> Unit,
) {
    UsScaffold(
        topBar = { UsTopBar(title = "Settings", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        Column(
            modifier = Modifier
                .padding(padding)
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = UsTheme.spacing.pageHorizontal),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xl),
        ) {
            UsSettingsSection("Your profile") {
                UsSettingsLinkRow(
                    "Identity and bio",
                    onClick = onEditProfile,
                    description = "Name, pronouns, profession, birthday, status and appearance"
                )
                UsSettingsLinkRow(
                    "About, education and links",
                    onClick = onProfileDetails,
                    description = "Work, education, hobbies, skills, languages and link-in-bio"
                )
            }
            UsSettingsSection("Controls") {
                UsSettingsLinkRow(
                    "Privacy and safety",
                    onClick = onPrivacy,
                    description = "Messages, calls, presence, discovery, abusive-content filtering and trusted circle"
                )
                UsSettingsLinkRow(
                    "Notifications",
                    onClick = onNotifications,
                    description = "Push, email, quiet hours and notification categories"
                )
                UsSettingsLinkRow(
                    "Account and security",
                    onClick = onSecurity,
                    description = "Sessions, two-factor authentication, trusted activity and account status"
                )
            }
        }
    }
}
