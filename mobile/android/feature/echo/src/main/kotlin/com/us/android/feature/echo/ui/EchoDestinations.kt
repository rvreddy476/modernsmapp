package com.us.android.feature.echo.ui

import com.us.android.core.model.NotificationTarget

/** Where Echo's screens go. Echo's own pages are Echo's; every way out of Echo is `:app`'s. */
data class EchoDestinations(
    val onBack: () -> Unit,
    val onOpenNote: (noteId: String) -> Unit,
    /** The person's VChat profile (Edit profile, and links from outside Echo). */
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
    // ── Echo v2 (14 Nov 2026): Echo's own pages ──
    val onOpenEchoProfile: (userId: String) -> Unit = onOpenProfile,
    val onOpenHashtag: (tag: String) -> Unit = {},
    val onOpenSearch: (query: String) -> Unit = {},
    val onOpenQuotes: (noteId: String) -> Unit = {},
    val onOpenBookmarks: () -> Unit = {},
    val onOpenScheduled: () -> Unit = {},
    // ── ways out, `:app`'s ──
    /** The app's chat inbox (the Chat item of Echo's bottom bar). */
    val onOpenChat: () -> Unit = {},
    /** The app's settings (the drawer's Settings). */
    val onOpenSettings: () -> Unit = {},
    /** Leaves Echo for VChat (the drawer's last row). */
    val onBackToVChat: () -> Unit = onBack,
    /** A notification row that is not an Echo note: `:app` opens it as its inbox would. */
    val onOpenNotification: (NotificationTarget) -> Unit = {},
)
