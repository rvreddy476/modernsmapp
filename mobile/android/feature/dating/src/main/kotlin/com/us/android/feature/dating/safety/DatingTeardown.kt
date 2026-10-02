package com.us.android.feature.dating.safety

import com.us.android.core.common.session.SessionTeardownTask
import com.us.android.feature.dating.data.DatingRepository
import javax.inject.Inject

/**
 * Sign-out: what the kind-message checks (mechanic M13) and screen protection
 * (mechanic M18) remember for one account is dropped before the next signs in —
 * which conversations are Pulse chats, the verdicts on received texts, and the
 * client config.
 */
class DatingTeardown @Inject constructor(
    private val repository: DatingRepository,
    private val kindness: DatingConversationKindness,
    private val screenProtection: ScreenProtectionConfig,
) : SessionTeardownTask {
    override suspend fun onSignOut() {
        repository.forgetConversations()
        kindness.forget()
        screenProtection.forget()
    }
}
