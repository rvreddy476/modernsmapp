package com.us.android.core.notifications.data

import android.content.Context
import androidx.core.app.NotificationManagerCompat
import com.us.android.core.common.session.SessionTeardownTask
import dagger.hilt.android.qualifiers.ApplicationContext
import javax.inject.Inject

/**
 * Clears every notification this app has posted, during sign-out (chat audit
 * L3).
 *
 * A posted notification outlives the session that received it: its tap intent
 * names the conversation, post or order of the account that was signed in.
 * Left in the shade, the next account on this handset can tap the previous
 * one's "New Message" into a thread it is no member of — and the shade itself
 * says that account had activity. Unregistering the push token
 * ([PushTeardown]) stops new ones; this removes the ones already shown.
 *
 * Local only (no network, no session state), so it cannot fail sign-out.
 */
class NotificationShadeTeardown @Inject constructor(
    @ApplicationContext private val context: Context,
) : SessionTeardownTask {

    override suspend fun onSignOut() {
        NotificationManagerCompat.from(context).cancelAll()
    }
}
