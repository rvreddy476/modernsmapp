package com.us.android.feature.echo.ui

/** Where Echo's screens go; `:app` decides each. */
data class EchoDestinations(
    val onBack: () -> Unit,
    val onOpenNote: (noteId: String) -> Unit,
    val onOpenProfile: (userId: String) -> Unit,
    val onCompose: (ComposeRequest) -> Unit,
    /**
     * Whether the premium sheet may offer "Get Premium" (2026-11-13). `:app`
     * answers true only while its purchase switch is on AND the server's
     * premium plans answered 200; read where the sheet is drawn. False (the
     * default) keeps the sheet as it was: OK only.
     */
    val premiumEntryShown: () -> Boolean = { false },
    /** Opens Momentum Premium; `:app` owns the route. */
    val onGetPremium: () -> Unit = {},
)
