package com.us.android.feature.dating.safety

import com.us.android.core.common.session.SessionTeardownTask
import com.us.android.feature.dating.DatingSession
import com.us.android.feature.dating.data.DatingRepository
import javax.inject.Inject

/**
 * What Dating keeps on this device for one account, dropped in one place
 * (audit L1) — at sign-out, and after the dating profile is deleted:
 *
 *  - the session: profile, consents, the people this account blocked or
 *    reported, and the mechanics switched off for it ([DatingSession.clear]);
 *  - which conversations are Pulse chats (mechanic M13);
 *  - the kind-message verdicts and the stored "did this bother you?" answers,
 *    on disk ([DatingConversationKindness.forget]);
 *  - the client config behind screen protection (mechanic M18).
 *
 * Sign-out does not end the process, so without this a second account on the
 * same phone would have the first one's blocks filtered out of its deck.
 */
class DatingTeardown @Inject constructor(
    private val repository: DatingRepository,
    private val kindness: DatingConversationKindness,
    private val screenProtection: ScreenProtectionConfig,
    private val session: DatingSession,
) : SessionTeardownTask {

    override suspend fun onSignOut() = forgetLocalData()

    /** Everything above, for this account. Safe to call more than once. */
    suspend fun forgetLocalData() {
        repository.forgetConversations()
        kindness.forget()
        screenProtection.forget()
        session.clear()
    }
}
