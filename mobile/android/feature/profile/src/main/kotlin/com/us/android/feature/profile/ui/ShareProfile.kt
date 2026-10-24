package com.us.android.feature.profile.ui

import android.content.ClipData
import android.content.Context
import android.content.Intent
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.platform.LocalClipboard
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.toClipEntry
import com.us.android.core.model.Profile
import com.us.android.core.model.WebLinks
import kotlinx.coroutines.launch

/**
 * Share profile (T-21, 2026-10-15): the profile's public web address,
 * `https://cleestudio.com/u/<username>` ([WebLinks.profileUrl]), through the
 * system share sheet. Offered to the owner (beside Edit profile) and to a
 * visitor (the More menu). The web page is a public prefix, so the
 * recipient needs no account to open it; there is no `/u/` App Link yet, so
 * a phone with the app opens it in the browser too.
 *
 * A no-op for a profile without a handle — the controls are not drawn then.
 */
@Composable
internal fun rememberProfileSharer(): (Profile) -> Unit {
    val context = LocalContext.current
    return remember(context) {
        fun(profile: Profile) {
            profileShareText(profile.nameForDisplay, profile.username)?.let(context::shareText)
        }
    }
}

/**
 * What Share profile puts in `EXTRA_TEXT`: the name, then the address on its
 * own line — last, so a messenger's link preview reads it. Null when the
 * profile has no handle to link to. Pure, so it is unit-tested.
 */
internal fun profileShareText(displayName: String, username: String): String? {
    // The web's share tail (`?ref=…&utm_source=share`) on top, like every link the web hands out.
    val url = WebLinks.profileUrl(username)?.let(WebLinks::withShareRef) ?: return null
    val name = displayName.trim()
    return if (name.isEmpty()) url else "$name\n\n$url"
}

/** Whether [profile] has an address to share at all. */
internal fun canShareProfile(profile: Profile): Boolean = WebLinks.profileUrl(profile.username) != null

private fun Context.shareText(text: String) {
    val send = Intent(Intent.ACTION_SEND).apply {
        type = "text/plain"
        putExtra(Intent.EXTRA_TEXT, text)
    }
    // createChooser, not the bare intent: without it Android can silently
    // reuse a previously chosen default target.
    startActivity(Intent.createChooser(send, null))
}

/**
 * Copy link (the profile More menu, 2026-10-24, the web's "Copy profile
 * link"): the same address Share profile hands out, on the clipboard. The
 * returned function answers whether there was an address to copy.
 */
@Composable
internal fun rememberProfileLinkCopier(): (Profile) -> Boolean {
    val clipboard = LocalClipboard.current
    val scope = rememberCoroutineScope()
    return remember(clipboard, scope) {
        fun(profile: Profile): Boolean {
            val link = profileLink(profile.username) ?: return false
            scope.launch { clipboard.setClipEntry(ClipData.newPlainText(PROFILE_LINK_LABEL, link).toClipEntry()) }
            return true
        }
    }
}

/** The profile's public address with the web's share tail, or null without a handle. Pure, so it is unit-tested. */
internal fun profileLink(username: String): String? = WebLinks.profileUrl(username)?.let(WebLinks::withShareRef)

private const val PROFILE_LINK_LABEL = "Profile link"

/** What Copy link says once the address is on the clipboard. */
internal const val LINK_COPIED = "Link copied"
