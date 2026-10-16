package com.us.android.feature.profile.ui

import android.content.Context
import android.content.Intent
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.platform.LocalContext
import com.us.android.core.model.Profile
import com.us.android.core.model.WebLinks

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
    val url = WebLinks.profileUrl(username) ?: return null
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
