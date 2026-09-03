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
import com.us.android.feature.profile.navigation.SettingsSections

/**
 * The TikTok/Instagram-style settings hub: one section per topic, one row per
 * page. [sections] resolves every row below "Your profile" — `:app` is the
 * only module that knows which feature (or cross-feature flow) each one opens.
 */
@Composable
fun SettingsHubScreen(
    onBack: () -> Unit,
    onEditProfile: () -> Unit,
    onProfileDetails: () -> Unit,
    sections: SettingsSections,
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
            UsSettingsSection("Account") {
                UsSettingsLinkRow(
                    "Manage account",
                    onClick = sections.onManageAccount,
                    description = "Email, phone, region, deactivation and deletion",
                )
                UsSettingsLinkRow(
                    "Modules and home page",
                    onClick = sections.onModules,
                    description = "Which parts of the app you use, and which one opens first",
                )
            }
            UsSettingsSection("Privacy and safety") {
                UsSettingsLinkRow(
                    "Privacy",
                    onClick = sections.onPrivacy,
                    description = "Private account, comments, messages, calls, presence and discovery",
                )
            }
            UsSettingsSection("Notifications") {
                UsSettingsLinkRow(
                    "Push notifications",
                    onClick = sections.onNotifications,
                    description = "In-app and push, per notification type",
                )
            }
            UsSettingsSection("Content and screen time") {
                UsSettingsLinkRow(
                    "Screen time",
                    onClick = sections.onScreenTime,
                    description = "Daily limit, sleep hours and this week's usage",
                )
                UsSettingsLinkRow(
                    "Content preferences",
                    onClick = sections.onContentPreferences,
                    description = "Keywords to filter out of your feed",
                )
            }
            UsSettingsSection("Your profile") {
                UsSettingsLinkRow(
                    "Identity and bio",
                    onClick = onEditProfile,
                    description = "Name, pronouns, profession, birthday, status and appearance",
                )
                UsSettingsLinkRow(
                    "About and links",
                    onClick = onProfileDetails,
                    description = "Work, education, hobbies, skills, languages and link-in-bio",
                )
            }
            UsSettingsSection("Security") {
                UsSettingsLinkRow(
                    "Account and security",
                    onClick = sections.onSecurity,
                    description = "Sessions, two-factor authentication and trusted activity",
                )
            }
        }
    }
}
