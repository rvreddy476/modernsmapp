package com.us.android.feature.profile.ui

import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.net.Uri
import com.us.android.core.model.WebLinks

/**
 * Whether [url] may be handed to the browser from Settings: an https link on
 * the web app's own hosts only, so a row can never open anything else.
 * Pure, so it is a table test.
 */
internal fun isSettingsWebLink(url: String): Boolean {
    val trimmed = url.trim()
    if (!trimmed.startsWith(HTTPS, ignoreCase = true)) return false
    val authority = trimmed.substring(HTTPS.length).substringBefore('/').substringBefore('?').substringBefore('#')
    return WebLinks.WEB_HOSTS.any { it.equals(authority, ignoreCase = true) }
}

/**
 * Opens one of Settings' web pages (Report a problem, Terms, Privacy policy)
 * in the phone's browser: a plain ACTION_VIEW, the way the app hands off
 * every other link (Custom Tabs is not in the build). False when the link
 * is not one of ours or nothing on the phone can open it; never throws.
 */
internal fun Context.openWebLink(url: String): Boolean {
    if (!isSettingsWebLink(url)) return false
    val intent = Intent(Intent.ACTION_VIEW, Uri.parse(url.trim()))
        .addCategory(Intent.CATEGORY_BROWSABLE)
        .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
    return try {
        startActivity(intent)
        true
    } catch (_: ActivityNotFoundException) {
        false
    } catch (_: SecurityException) {
        false
    }
}

private const val HTTPS = "https://"
