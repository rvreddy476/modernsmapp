package com.us.android.feature.echo.ui

/** Where Echo's screens go; `:app` decides each. */
data class EchoDestinations(
    val onBack: () -> Unit,
    val onOpenNote: (noteId: String) -> Unit,
    val onOpenProfile: (userId: String) -> Unit,
    val onCompose: (ComposeRequest) -> Unit,
)
