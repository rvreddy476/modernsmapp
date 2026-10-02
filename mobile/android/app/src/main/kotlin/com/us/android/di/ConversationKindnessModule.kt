package com.us.android.di

import com.us.android.core.common.chat.ConversationKindness
import com.us.android.feature.dating.safety.DatingConversationKindness
import dagger.Binds
import dagger.Module
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent

/**
 * Kind messages (Pulse mechanic M13): chat asks [ConversationKindness] before
 * a send and about each received text; Dating answers for its own
 * conversations and says "nothing to do" for every other one. Bound here
 * because only `:app` may see both features.
 */
@Module
@InstallIn(SingletonComponent::class)
abstract class ConversationKindnessModule {

    @Binds
    abstract fun bindConversationKindness(impl: DatingConversationKindness): ConversationKindness
}
