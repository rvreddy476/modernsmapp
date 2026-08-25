package com.us.android.core.notifications

import javax.inject.Inject
import javax.inject.Singleton

/**
 * Whether the app currently has a visible activity.
 *
 * Written by the application's ProcessLifecycleOwner observer, read by
 * [NotificationPresenter] from the FCM callback thread — a plain volatile
 * flag on purpose, because the presenter must not touch a main-thread-only
 * lifecycle object from a binder thread.
 *
 * Why it matters: while the user is IN the app the session socket already
 * delivers every chat message to the open surface, so a system notification
 * for the same message is a duplicate that arrives a beat later. Foreground
 * suppression is the client half of push/socket de-duplication; backgrounded,
 * the system-rendered push is the only channel and always shows.
 */
@Singleton
class AppForegroundState @Inject constructor() {
    @Volatile
    var isForeground: Boolean = false
}
