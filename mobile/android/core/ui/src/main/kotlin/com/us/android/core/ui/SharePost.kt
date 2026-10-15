package com.us.android.core.ui

import android.content.Context
import android.content.Intent
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.platform.LocalContext

/**
 * Hands a post to the system share sheet.
 *
 * WHAT IS SHARED
 *
 * The text and, when it is known, the author — as plain text — followed by
 * the post's public permalink ([postShareLink], `https://cleestudio.com/post/<id>`),
 * the same address "Copy link" puts on the clipboard. The recipient's browser
 * opens the web post page; a phone with the app opens the post detail through
 * the manifest's App Link. Until 2026-10-14 the share carried no URL at all,
 * so a recipient had words and no way to reach the post (audit T-07).
 *
 * Uses the system chooser rather than a bespoke sheet: users already know it,
 * it ranks targets by their own habits, and every app they might send to is
 * reachable without this app enumerating installed packages.
 */
@Composable
fun rememberPostSharer(): (postId: String, text: String, authorName: String?) -> Unit {
    val context = LocalContext.current
    return remember(context) { { postId, text, author -> context.sharePost(postShareIntent(postId, text, author)) } }
}

/** The ACTION_SEND intent the chooser wraps; [postShareText] is its EXTRA_TEXT. */
internal fun postShareIntent(postId: String, text: String, authorName: String?): Intent =
    Intent(Intent.ACTION_SEND).apply {
        type = "text/plain"
        putExtra(Intent.EXTRA_TEXT, postShareText(postId, text, authorName))
    }

/**
 * What [rememberPostSharer] puts in [Intent.EXTRA_TEXT]: the quote, the
 * attribution, then the permalink on its own line — last, so a messenger's
 * link preview reads it and a long quote cannot push it out of sight.
 *
 * [authorName] is nullable because not every payload carries one — the post
 * detail response sends only an author id. Attributing a quote to a UUID would
 * be worse than not attributing it at all. Pure, so it is unit-tested.
 */
fun postShareText(postId: String, text: String, authorName: String?): String {
    val author = authorName?.takeIf { it.isNotBlank() }
    val quote = when {
        author == null -> text.trim()
        text.isBlank() -> author
        else -> "${text.trim()}\n\n— $author"
    }
    val link = postShareLink(postId)
    return if (quote.isBlank()) link else "$quote\n\n$link"
}

private fun Context.sharePost(send: Intent) {
    // createChooser, not the bare intent: without it Android can silently
    // reuse a previously chosen default, turning "share" into "send to
    // whatever I picked once, months ago".
    startActivity(Intent.createChooser(send, null))
}
