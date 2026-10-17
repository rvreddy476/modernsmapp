package com.us.android.feature.profile.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.AppearanceMode
import com.us.android.core.model.WebLinks
import com.us.android.feature.profile.navigation.SettingsSections

/**
 * The groups of the Settings & privacy page (2026-10-17), in the order they
 * are drawn. Log out and the version line sit under the last one, outside
 * any group. The web settings hub uses the same six.
 */
enum class SettingsGroup(val title: String) {
    ACCOUNT("Account"),
    PRIVACY("Privacy & safety"),
    NOTIFICATIONS("Notifications"),
    CONTENT("Content & display"),
    DATA("Data & storage"),
    SUPPORT("Support & about"),
}

/**
 * Every row of the page, in the order it is drawn within its [group]. A row
 * with a [webUrl] opens that public page in the browser; [APPEARANCE] acts
 * in place (its card holds the System / Light / Dark switch); every other
 * row is a screen `:app` resolves ([settingsRowAction]).
 */
enum class SettingsRow(
    val group: SettingsGroup,
    val title: String,
    val subtitle: String?,
    val icon: ImageVector,
    val webUrl: String? = null,
) {
    MANAGE_ACCOUNT(SettingsGroup.ACCOUNT, "Manage account", "Email, phone, region and deletion", UsIcons.Profile),
    IDENTITY_AND_BIO(
        SettingsGroup.ACCOUNT,
        "Identity and bio",
        "Name, pronouns, profession and status",
        UsIcons.SquarePen,
    ),
    ABOUT_AND_LINKS(SettingsGroup.ACCOUNT, "About and links", "Work, education, skills and links", UsIcons.Link),
    ACCOUNT_AND_SECURITY(
        SettingsGroup.ACCOUNT,
        "Account and security",
        "Sessions, two-factor and trusted devices",
        UsIcons.ShieldCheck,
    ),
    MODULES(SettingsGroup.ACCOUNT, "Modules and home page", "Which apps you use and which opens first", UsIcons.Home),
    RECENTLY_DELETED(SettingsGroup.ACCOUNT, "Recently deleted", "Restore posts from the last 30 days", UsIcons.Trash),
    PRIVACY(SettingsGroup.PRIVACY, "Privacy", "Private account, messages, calls and discovery", UsIcons.Lock),
    PUSH_NOTIFICATIONS(
        SettingsGroup.NOTIFICATIONS,
        "Push notifications",
        "In-app and push, per type",
        UsIcons.Notifications,
    ),
    APPEARANCE(SettingsGroup.CONTENT, "Appearance", null, UsIcons.Moon),
    SCREEN_TIME(SettingsGroup.CONTENT, "Screen time", "Daily limit, sleep hours and weekly usage", UsIcons.Clock),
    CONTENT_PREFERENCES(
        SettingsGroup.CONTENT,
        "Content preferences",
        "Keywords to filter out of your feed",
        UsIcons.Sliders,
    ),
    OFFLINE_VIDEOS(SettingsGroup.DATA, "Offline videos", "Videos kept on this device", UsIcons.ArrowDownToLine),
    REPORT_PROBLEM(SettingsGroup.SUPPORT, "Report a problem", null, UsIcons.Flag, WebLinks.GRIEVANCE_URL),
    TERMS(SettingsGroup.SUPPORT, "Terms & policies", null, UsIcons.FileText, WebLinks.TERMS_URL),
    PRIVACY_POLICY(SettingsGroup.SUPPORT, "Privacy policy", null, UsIcons.Shield, WebLinks.PRIVACY_POLICY_URL),
}

/** The page, grouped: every group in order, each with its rows in order. Pure, so it is a table test. */
fun settingsHubGroups(): List<Pair<SettingsGroup, List<SettingsRow>>> =
    SettingsGroup.entries.map { group -> group to SettingsRow.entries.filter { it.group == group } }

/**
 * What tapping [row] does: the screen `:app` gave for it, or the row's web
 * page through [openWeb]. Null for [SettingsRow.APPEARANCE], whose card is
 * its own control.
 */
internal fun settingsRowAction(
    row: SettingsRow,
    sections: SettingsSections,
    onEditProfile: () -> Unit,
    onProfileDetails: () -> Unit,
    openWeb: (url: String) -> Unit,
): (() -> Unit)? {
    row.webUrl?.let { url -> return { openWeb(url) } }
    return when (row) {
        SettingsRow.IDENTITY_AND_BIO -> onEditProfile
        SettingsRow.ABOUT_AND_LINKS -> onProfileDetails
        else -> sections.screenFor(row)
    }
}

/** The `:app`-resolved screen behind [row]; null for Appearance and the web rows. */
private fun SettingsSections.screenFor(row: SettingsRow): (() -> Unit)? = when (row) {
    SettingsRow.MANAGE_ACCOUNT -> onManageAccount
    SettingsRow.ACCOUNT_AND_SECURITY -> onSecurity
    SettingsRow.MODULES -> onModules
    SettingsRow.RECENTLY_DELETED -> onRecentlyDeleted
    SettingsRow.PRIVACY -> onPrivacy
    SettingsRow.PUSH_NOTIFICATIONS -> onNotifications
    SettingsRow.SCREEN_TIME -> onScreenTime
    SettingsRow.CONTENT_PREFERENCES -> onContentPreferences
    SettingsRow.OFFLINE_VIDEOS -> onOfflineVideos
    else -> null
}

/** What the version line reads; nothing when the build gave no version. */
internal fun settingsVersionLine(versionName: String): String? =
    versionName.trim().takeIf { it.isNotEmpty() }?.let { "Momentum $it" }

/**
 * Settings & privacy (2026-10-17): grouped rounded cards on the page ground,
 * each row a glyph in a soft square, a title, an optional one-line subtitle
 * and a chevron; then Log out, in red, behind a confirm; then the version.
 * [sections] resolves the rows that leave this feature — `:app` is the only
 * module that knows which feature (or cross-feature flow) each one opens.
 */
@Composable
fun SettingsHubScreen(
    onBack: () -> Unit,
    onEditProfile: () -> Unit,
    onProfileDetails: () -> Unit,
    sections: SettingsSections,
    /** The Appearance setting (T-32): the one picked, shown selected. */
    appearance: AppearanceMode = AppearanceMode.SYSTEM,
    onAppearance: (AppearanceMode) -> Unit = {},
    /** The app's version name, for the line at the bottom; blank draws none. */
    versionName: String = "",
    logout: SettingsLogoutUiState = SettingsLogoutUiState(),
    onLogout: () -> Unit = {},
    onConfirmLogout: () -> Unit = {},
    onCancelLogout: () -> Unit = {},
) {
    val context = LocalContext.current
    val openWeb: (String) -> Unit = { url -> context.openWebLink(url) }
    UsScaffold(
        topBar = { UsTopBar(title = "Settings & privacy", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        Column(
            modifier = Modifier
                .padding(padding)
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .testTag("settings_hub"),
        ) {
            settingsHubGroups().forEach { (group, rows) ->
                GroupHeading(group.title)
                SettingsCard {
                    rows.forEachIndexed { index, row ->
                        if (index > 0) RowDivider()
                        if (row == SettingsRow.APPEARANCE) {
                            AppearanceRow(appearance = appearance, onAppearance = onAppearance)
                        } else {
                            SettingsLinkRow(
                                row = row,
                                onClick = settingsRowAction(row, sections, onEditProfile, onProfileDetails, openWeb),
                            )
                        }
                    }
                }
            }
            Spacer(Modifier.height(UsTheme.spacing.xxxxl))
            SettingsCard {
                LogoutRow(enabled = !logout.busy, onClick = onLogout)
            }
            settingsVersionLine(versionName)?.let { line ->
                Text(
                    text = line,
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                    textAlign = TextAlign.Center,
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(vertical = UsTheme.spacing.xxxxl)
                        .testTag("settings_version"),
                )
            }
            Spacer(Modifier.height(UsTheme.spacing.xxxxl))
        }
    }

    if (logout.confirming) {
        LogoutDialog(onConfirm = onConfirmLogout, onDismiss = onCancelLogout)
    }
}

/** The appearance choice's line under the switch: what it does, in a few words. */
internal fun appearanceDescription(mode: AppearanceMode): String = when (mode) {
    AppearanceMode.SYSTEM -> "Follow this device's light or dark setting"
    AppearanceMode.LIGHT -> "Always light"
    AppearanceMode.DARK -> "Always dark"
}

@Composable
private fun GroupHeading(title: String) {
    Text(
        text = title,
        style = MaterialTheme.typography.labelLarge,
        color = UsTheme.extended.textMuted,
        modifier = Modifier
            .padding(start = UsTheme.spacing.xs, top = UsTheme.spacing.xxxxl, bottom = UsTheme.spacing.m)
            .semantics { heading() },
    )
}

/** One group's card: the raised surface (white on light, lifted on dark), rounded, rows inside. */
@Composable
private fun SettingsCard(content: @Composable ColumnScope.() -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(UsTheme.radii.large))
            .background(UsTheme.extended.bgSheet),
        content = content,
    )
}

@Composable
private fun RowDivider() {
    HorizontalDivider(
        color = UsTheme.extended.borderSubtle,
        // Starts under the title: the row inset, the glyph square and the gap after it.
        modifier = Modifier.padding(start = UsTheme.spacing.xxl + ICON_SQUARE + UsTheme.spacing.l),
    )
}

@Composable
private fun SettingsLinkRow(row: SettingsRow, onClick: (() -> Unit)?) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .heightIn(min = ROW_MIN_HEIGHT)
            .clickable(enabled = onClick != null, role = Role.Button, onClickLabel = row.title) { onClick?.invoke() }
            .padding(horizontal = UsTheme.spacing.xxl, vertical = UsTheme.spacing.l)
            .testTag("settings_row:${row.name.lowercase()}"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        RowGlyph(row.icon, UsTheme.extended.textPrimary)
        Column(modifier = Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs / 2)) {
            Text(
                text = row.title,
                style = MaterialTheme.typography.bodyLarge,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            row.subtitle?.let {
                Text(
                    text = it,
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
        Icon(
            imageVector = UsIcons.ChevronRight,
            contentDescription = null,
            tint = UsTheme.extended.textMuted,
            modifier = Modifier.size(CHEVRON),
        )
    }
}

@Composable
private fun RowGlyph(icon: ImageVector, tint: Color) {
    Box(
        modifier = Modifier
            .size(ICON_SQUARE)
            .background(UsTheme.extended.fillSubtle, RoundedCornerShape(UsTheme.radii.medium)),
        contentAlignment = Alignment.Center,
    ) {
        Icon(imageVector = icon, contentDescription = null, tint = tint, modifier = Modifier.size(ROW_GLYPH))
    }
}

/**
 * Appearance (T-32), in its card: System / Light / Dark as one segmented
 * switch, applied the moment a segment is picked; the app re-themes under
 * the viewer's thumb. The line under it says what the pick does.
 */
@Composable
private fun AppearanceRow(appearance: AppearanceMode, onAppearance: (AppearanceMode) -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.xxl, vertical = UsTheme.spacing.l)
            .testTag("settings_row:appearance"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            RowGlyph(SettingsRow.APPEARANCE.icon, UsTheme.extended.textPrimary)
            Column(modifier = Modifier.weight(1f)) {
                Text(
                    text = SettingsRow.APPEARANCE.title,
                    style = MaterialTheme.typography.bodyLarge,
                    color = UsTheme.extended.textPrimary,
                )
                Text(
                    text = appearanceDescription(appearance),
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .clip(RoundedCornerShape(UsTheme.radii.full))
                .background(UsTheme.extended.fillSubtle)
                .padding(UsTheme.spacing.xs)
                .selectableGroup(),
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
        ) {
            AppearanceMode.entries.forEach { mode ->
                val selected = mode == appearance
                Box(
                    modifier = Modifier
                        .weight(1f)
                        .heightIn(min = SEGMENT_HEIGHT)
                        .clip(RoundedCornerShape(UsTheme.radii.full))
                        .background(if (selected) UsTheme.extended.selectedPill else Color.Transparent)
                        .selectable(selected = selected, role = Role.RadioButton, onClick = { onAppearance(mode) })
                        .testTag("settings_appearance:${mode.wire}"),
                    contentAlignment = Alignment.Center,
                ) {
                    Text(
                        text = mode.label,
                        style = MaterialTheme.typography.labelLarge,
                        color = if (selected) UsTheme.extended.onSelectedPill else UsTheme.extended.textSecondary,
                    )
                }
            }
        }
    }
}

@Composable
private fun LogoutRow(enabled: Boolean, onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .heightIn(min = ROW_MIN_HEIGHT)
            .clickable(enabled = enabled, role = Role.Button, onClickLabel = LOG_OUT, onClick = onClick)
            .padding(horizontal = UsTheme.spacing.xxl, vertical = UsTheme.spacing.l)
            .testTag("settings_row:log_out"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        RowGlyph(UsIcons.LogOut, UsTheme.extended.statusDanger)
        Text(
            text = LOG_OUT,
            style = MaterialTheme.typography.bodyLarge,
            color = UsTheme.extended.statusDanger,
            modifier = Modifier.weight(1f),
        )
    }
}

/** "Log out of Momentum on this device?" — nothing signs out until it is confirmed. */
@Composable
private fun LogoutDialog(onConfirm: () -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Log out of Momentum on this device?") },
        confirmButton = {
            TextButton(onClick = onConfirm, modifier = Modifier.testTag("settings_logout_confirm")) {
                Text(LOG_OUT, color = UsTheme.extended.statusDanger)
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) { Text("Cancel", color = UsTheme.extended.textPrimary) }
        },
        containerColor = UsTheme.extended.bgSheet,
        titleContentColor = UsTheme.extended.textPrimary,
    )
}

private const val LOG_OUT = "Log out"
private val ROW_MIN_HEIGHT = 56.dp
private val SEGMENT_HEIGHT = 36.dp
private val ICON_SQUARE = 36.dp
private val ROW_GLYPH = 20.dp
private val CHEVRON = 18.dp
