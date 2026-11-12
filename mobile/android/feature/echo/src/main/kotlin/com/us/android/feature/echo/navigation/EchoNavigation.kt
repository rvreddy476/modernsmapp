package com.us.android.feature.echo.navigation

import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import androidx.navigation.toRoute
import com.us.android.feature.echo.ui.ComposeMode
import com.us.android.feature.echo.ui.ComposeRequest
import com.us.android.feature.echo.ui.ComposerScreen
import com.us.android.feature.echo.ui.EchoDestinations
import com.us.android.feature.echo.ui.EchoHomeScreen
import com.us.android.feature.echo.ui.NoteDetailScreen
import kotlinx.serialization.Serializable

/** Echo's home: Following and For You. From the launcher tile and Home's shortcut. */
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
 * to, quoted or edited (empty for a new note). Only opened after the
 * eligibility read let it through; the composer reads it again itself.
 */
@Serializable
data class EchoComposeRoute(val mode: String = ComposeMode.NEW.wire, val targetId: String = "")

/** Registers Echo's three screens. Every way out is `:app`'s. */
fun NavGraphBuilder.echoScreens(
    onBack: () -> Unit,
    onOpenProfile: (userId: String) -> Unit,
    navController: NavController,
) {
    val destinations = EchoDestinations(
        onBack = onBack,
        onOpenNote = { navController.navigateToEchoNote(it) },
        onOpenProfile = onOpenProfile,
        onCompose = { navController.navigateToEchoComposer(it) },
    )
    composable<EchoHomeRoute> { EchoHomeScreen(destinations = destinations) }
    composable<EchoNoteRoute> { NoteDetailScreen(destinations = destinations) }
    composable<EchoComposeRoute> { entry ->
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
        )
    }
}

fun NavController.navigateToEcho() = navigate(EchoHomeRoute) { launchSingleTop = true }

fun NavController.navigateToEchoNote(noteId: String) = navigate(EchoNoteRoute(noteId))

fun NavController.navigateToEchoComposer(request: ComposeRequest) =
    navigate(EchoComposeRoute(mode = request.mode.wire, targetId = request.targetId))
