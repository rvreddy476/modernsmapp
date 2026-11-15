package com.us.android.feature.echo.navigation

import androidx.compose.ui.window.DialogProperties
import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import androidx.navigation.compose.dialog
import androidx.navigation.toRoute
import com.us.android.core.model.NotificationTarget
import com.us.android.feature.echo.ui.ComposeMode
import com.us.android.feature.echo.ui.ComposeRequest
import com.us.android.feature.echo.ui.ComposerScreen
import com.us.android.feature.echo.ui.EchoDestinations
import com.us.android.feature.echo.ui.EchoListKind
import com.us.android.feature.echo.ui.EchoListScreen
import com.us.android.feature.echo.ui.EchoProfileScreen
import com.us.android.feature.echo.ui.EchoShell
import com.us.android.feature.echo.ui.NoteDetailScreen
import com.us.android.feature.echo.ui.ScheduledScreen
import kotlinx.serialization.Serializable

/**
 * Echo's own app (Echo v2, 14 Nov 2026): Home (For you / Following),
 * Explore and Notifications behind its own bottom bar, the drawer, and the
 * compose button. From the launcher tile and Home's shortcut. It has its
 * own frame: the VChat bar is not drawn over it (a pushed route).
 */
@Serializable
data object EchoHomeRoute

/**
 * One note. From a card, a profile's Echo tab, a `note_reply` / `note_quote`
 * push (`momentum://echo/note/{id}`) and the web link `/echo/note/{id}`.
 * The argument's name is [com.us.android.feature.echo.ui.NoteDetailViewModel.NOTE_ID_KEY].
 */
@Serializable
data class EchoNoteRoute(val noteId: String)

/**
 * The composer. [mode] is a [ComposeMode.wire]; [targetId] the note replied
 * to, quoted or edited (empty for a new note); [thread] starts a new post as
 * two entries. Only opened after the eligibility read let it through; the
 * composer reads it again itself.
 */
@Serializable
data class EchoComposeRoute(
    val mode: String = ComposeMode.NEW.wire,
    val targetId: String = "",
    val thread: Boolean = false,
)

/** Echo's profile page for [userId] (X's: Posts, Replies, Reposts, Media). */
@Serializable
data class EchoProfileRoute(val userId: String)

/** A hashtag, search results, Bookmarks or a note's Quotes: [kind] is an [EchoListKind.wire]. */
@Serializable
data class EchoListRoute(val kind: String, val arg: String = "")

/** The author's scheduled posts. */
@Serializable
data object EchoScheduledRoute

/** Registers Echo's screens. Every way out of Echo is `:app`'s. */
@Suppress("LongParameterList", "LongMethod") // :app's ways out, one parameter each
fun NavGraphBuilder.echoScreens(
    onBack: () -> Unit,
    onOpenProfile: (userId: String) -> Unit,
    navController: NavController,
    // Momentum Premium (2026-11-13): the premium sheet's "Get Premium", drawn only
    // while :app says so (its purchase switch on AND premium plans answered 200).
    premiumEntryShown: () -> Boolean = { false },
    onGetPremium: () -> Unit = {},
    // Echo v2 (14 Nov 2026): the bottom bar's Chat, the drawer's Settings and
    // Back to VChat, and a notification row that is not an Echo note.
    onOpenChat: () -> Unit = {},
    onOpenSettings: () -> Unit = {},
    onBackToVChat: () -> Unit = onBack,
    onOpenNotification: (NotificationTarget) -> Unit = {},
) {
    val destinations = EchoDestinations(
        onBack = onBack,
        onOpenNote = { navController.navigateToEchoNote(it) },
        onOpenProfile = onOpenProfile,
        onCompose = { navController.navigateToEchoComposer(it) },
        premiumEntryShown = premiumEntryShown,
        onGetPremium = onGetPremium,
        onOpenEchoProfile = { navController.navigate(EchoProfileRoute(it)) },
        onOpenHashtag = { navController.navigate(EchoListRoute(EchoListKind.HASHTAG.wire, it.removePrefix("#"))) },
        onOpenSearch = { query ->
            if (query.isNotBlank()) navController.navigate(EchoListRoute(EchoListKind.SEARCH.wire, query.trim()))
        },
        onOpenQuotes = { navController.navigate(EchoListRoute(EchoListKind.QUOTES.wire, it)) },
        onOpenBookmarks = { navController.navigate(EchoListRoute(EchoListKind.BOOKMARKS.wire)) },
        onOpenScheduled = { navController.navigate(EchoScheduledRoute) { launchSingleTop = true } },
        onOpenChat = onOpenChat,
        onOpenSettings = onOpenSettings,
        onBackToVChat = onBackToVChat,
        onOpenNotification = { target ->
            if (target is NotificationTarget.EchoNote) {
                navController.navigateToEchoNote(
                    target.noteId
                )
            } else {
                onOpenNotification(target)
            }
        },
    )
    composable<EchoHomeRoute> { EchoShell(destinations = destinations) }
    composable<EchoNoteRoute> { NoteDetailScreen(destinations = destinations) }
    composable<EchoProfileRoute> { EchoProfileScreen(destinations = destinations) }
    composable<EchoListRoute> { EchoListScreen(destinations = destinations) }
    composable<EchoScheduledRoute> { ScheduledScreen(destinations = destinations) }
    // A dialog destination: the composer is a sheet over the page it was opened from (look v3 §3).
    dialog<EchoComposeRoute>(
        dialogProperties = DialogProperties(usePlatformDefaultWidth = false, decorFitsSystemWindows = false),
    ) { entry ->
        val mode = ComposeMode.fromWire(entry.toRoute<EchoComposeRoute>().mode)
        ComposerScreen(
            onClose = onBack,
            onPosted = { noteId ->
                if (mode == ComposeMode.REPLY || mode == ComposeMode.EDIT) {
                    // Back to the note the reply or the edit was made from; it reads itself again.
                    onBack()
                } else {
                    // A new note, thread or quote replaces the composer, so Back returns to where it was opened.
                    navController.navigate(EchoNoteRoute(noteId)) { popUpTo<EchoComposeRoute> { inclusive = true } }
                }
            },
            onScheduled = {
                navController.navigate(EchoScheduledRoute) { popUpTo<EchoComposeRoute> { inclusive = true } }
            },
            premiumEntryShown = premiumEntryShown,
            onGetPremium = onGetPremium,
        )
    }
}

fun NavController.navigateToEcho() = navigate(EchoHomeRoute) { launchSingleTop = true }

fun NavController.navigateToEchoNote(noteId: String) = navigate(EchoNoteRoute(noteId))

fun NavController.navigateToEchoComposer(request: ComposeRequest) =
    navigate(EchoComposeRoute(mode = request.mode.wire, targetId = request.targetId, thread = request.thread))
