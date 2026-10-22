package com.us.android.feature.profile.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.feature.profile.navigation.SettingsSections
import org.junit.Test

/**
 * The Settings & privacy page (2026-10-17): grouped cards in the web hub's
 * order, every destination the old plain-row hub had still reachable, the
 * three public web pages under Support & about, and the version line.
 */
class SettingsHubTest {

    @Test
    fun `the groups are in the web hub's order`() {
        assertThat(settingsHubGroups().map { it.first.title })
            .containsExactly(
                "Account",
                "Privacy & safety",
                "Inbox & messaging",
                "Notifications",
                "Content & display",
                "Data & storage",
                "Support & about",
            )
            .inOrder()
    }

    @Test
    fun `each group's rows are in the order asked for`() {
        val rows = settingsHubGroups().associate { (group, rows) -> group to rows }

        assertThat(rows.getValue(SettingsGroup.ACCOUNT)).containsExactly(
            SettingsRow.MANAGE_ACCOUNT,
            SettingsRow.IDENTITY_AND_BIO,
            SettingsRow.ABOUT_AND_LINKS,
            SettingsRow.ACCOUNT_AND_SECURITY,
            SettingsRow.MODULES,
            SettingsRow.RECENTLY_DELETED,
        ).inOrder()
        assertThat(rows.getValue(SettingsGroup.PRIVACY)).containsExactly(SettingsRow.PRIVACY, SettingsRow.BLOCKED_ACCOUNTS).inOrder()
        // Chat parity (2026-10-18): its own group, its own page.
        assertThat(rows.getValue(SettingsGroup.INBOX)).containsExactly(SettingsRow.INBOX_MESSAGING)
        assertThat(rows.getValue(SettingsGroup.NOTIFICATIONS)).containsExactly(SettingsRow.PUSH_NOTIFICATIONS)
        assertThat(rows.getValue(SettingsGroup.CONTENT)).containsExactly(
            SettingsRow.APPEARANCE,
            SettingsRow.SCREEN_TIME,
            SettingsRow.CONTENT_PREFERENCES,
            SettingsRow.HIDDEN_CHANNELS,
        ).inOrder()
        // No Data saver row: Android has no such screen, and a row that opens nothing is never drawn.
        assertThat(rows.getValue(SettingsGroup.DATA)).containsExactly(SettingsRow.OFFLINE_VIDEOS)
        assertThat(rows.getValue(SettingsGroup.SUPPORT)).containsExactly(
            SettingsRow.REPORT_PROBLEM,
            SettingsRow.TERMS,
            SettingsRow.PRIVACY_POLICY,
        ).inOrder()
    }

    @Test
    fun `every row is drawn exactly once`() {
        val drawn = settingsHubGroups().flatMap { it.second }
        assertThat(drawn).containsNoDuplicates()
        assertThat(drawn).containsExactlyElementsIn(SettingsRow.entries)
    }

    @Test
    fun `every destination the old hub had is still behind a row, and Offline videos is new`() {
        val opened = mutableListOf<String>()
        val sections = SettingsSections(
            onManageAccount = { opened += "manage account" },
            onPrivacy = { opened += "privacy" },
            onNotifications = { opened += "notifications" },
            onScreenTime = { opened += "screen time" },
            onContentPreferences = { opened += "content preferences" },
            onRecentlyDeleted = { opened += "recently deleted" },
            onSecurity = { opened += "security" },
            onModules = { opened += "modules" },
            onOfflineVideos = { opened += "offline videos" },
            onInboxMessaging = { opened += "inbox and messaging" },
            onBlockedAccounts = { opened += "blocked accounts" },
            onHiddenChannels = { opened += "hidden channels" },
        )
        val web = mutableListOf<String>()

        SettingsRow.entries.forEach { row ->
            settingsRowAction(
                row = row,
                sections = sections,
                onEditProfile = { opened += "identity and bio" },
                onProfileDetails = { opened += "about and links" },
                openWeb = { web += it },
            )?.invoke()
        }

        assertThat(opened).containsExactly(
            "manage account",
            "identity and bio",
            "about and links",
            "security",
            "modules",
            "recently deleted",
            "privacy",
            "blocked accounts",
            "inbox and messaging",
            "notifications",
            "screen time",
            "content preferences",
            "hidden channels",
            "offline videos",
        ).inOrder()
        assertThat(web).hasSize(3)
    }

    @Test
    fun `Appearance is its own control, not a link`() {
        val sections = SettingsSections({}, {}, {}, {}, {}, {}, {}, {}, {})
        assertThat(settingsRowAction(SettingsRow.APPEARANCE, sections, {}, {}, {})).isNull()
        assertThat(SettingsRow.APPEARANCE.group).isEqualTo(SettingsGroup.CONTENT)
    }

    @Test
    fun `the support rows open the web app's public pages`() {
        val sections = SettingsSections({}, {}, {}, {}, {}, {}, {}, {}, {})
        val opened = mutableMapOf<SettingsRow, String>()
        listOf(SettingsRow.REPORT_PROBLEM, SettingsRow.TERMS, SettingsRow.PRIVACY_POLICY).forEach { row ->
            settingsRowAction(row, sections, {}, {}, { opened[row] = it })?.invoke()
        }

        assertThat(opened).isEqualTo(
            mapOf(
                SettingsRow.REPORT_PROBLEM to "https://cleestudio.com/grievance",
                SettingsRow.TERMS to "https://cleestudio.com/terms",
                SettingsRow.PRIVACY_POLICY to "https://cleestudio.com/privacy",
            ),
        )
        assertThat(SettingsRow.entries.filter { it.webUrl != null })
            .containsExactly(SettingsRow.REPORT_PROBLEM, SettingsRow.TERMS, SettingsRow.PRIVACY_POLICY)
        assertThat(opened.values.all(::isSettingsWebLink)).isTrue()
    }

    @Test
    fun `only the web app's own https pages may be handed to the browser`() {
        assertThat(isSettingsWebLink("https://cleestudio.com/terms")).isTrue()
        assertThat(isSettingsWebLink("https://app.cleestudio.com/privacy")).isTrue()
        assertThat(isSettingsWebLink("https://CLEESTUDIO.com")).isTrue()
        assertThat(isSettingsWebLink("http://cleestudio.com/terms")).isFalse()
        assertThat(isSettingsWebLink("https://cleestudio.com.evil.example/terms")).isFalse()
        assertThat(isSettingsWebLink("https://evil.example/?cleestudio.com")).isFalse()
        assertThat(isSettingsWebLink("intent://cleestudio.com")).isFalse()
        assertThat(isSettingsWebLink("")).isFalse()
    }

    @Test
    fun `the version line names the build, and is left out without one`() {
        assertThat(settingsVersionLine("0.1.0")).isEqualTo("Momentum 0.1.0")
        assertThat(settingsVersionLine(" 0.1.0-dev ")).isEqualTo("Momentum 0.1.0-dev")
        assertThat(settingsVersionLine("")).isNull()
        assertThat(settingsVersionLine("  ")).isNull()
    }

    @Test
    fun `every row says what it is, and subtitles stay one short line`() {
        SettingsRow.entries.forEach { row ->
            assertThat(row.title).isNotEmpty()
            row.subtitle?.let { assertThat(it.length).isAtMost(MAX_SUBTITLE) }
        }
    }

    private companion object {
        const val MAX_SUBTITLE = 48
    }
}
