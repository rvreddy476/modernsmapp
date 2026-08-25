package com.us.android.core.chat.di

import android.content.Context
import androidx.work.WorkManager
import com.us.android.core.chat.data.ChatApi
import com.us.android.core.chat.data.ChatSendWorker
import com.us.android.core.chat.data.ChatSocket
import com.us.android.core.chat.data.ChatStore
import com.us.android.core.chat.data.OutboxScheduler
import com.us.android.core.network.ApiConfig
import com.us.android.core.network.di.AuthenticatedClient
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import okhttp3.OkHttpClient
import retrofit2.Retrofit
import javax.inject.Singleton

/**
 * Creates the chat endpoints and the socket from the app-wide instances.
 *
 * No client, no base URL, no converter of its own. A module that assembles its
 * own client forks token refresh, and two refreshers racing a rotating refresh
 * token sign the user out.
 */
@Module
@InstallIn(SingletonComponent::class)
object ChatModule {

    @Provides
    @Singleton
    fun provideChatApi(retrofit: Retrofit): ChatApi = retrofit.create(ChatApi::class.java)

    /**
     * The socket shares the AUTHENTICATED OkHttp client.
     *
     * Same client as every other request, so the socket inherits single-flight
     * token refresh and the origin-scoped bearer. A second stack would hold
     * its own stale token and race the rotation.
     *
     * The URL comes from [ApiConfig.wsBaseUrl] rather than being derived from
     * the HTTP base: the two are separately configurable per flavor, and
     * guessing one from the other silently breaks whichever environment does
     * not follow the pattern.
     */
    @Provides
    @Singleton
    fun provideChatSocket(
        @AuthenticatedClient client: OkHttpClient,
        config: ApiConfig,
    ): ChatSocket = ChatSocket(client = client, wsBaseUrl = config.wsBaseUrl)

    /**
     * WorkManager for the durable send outbox. `getInstance` is safe here —
     * the app removes the default initializer and provides on-demand Hilt
     * configuration (see the Studio's PublishWorker, which established the
     * pattern).
     */
    @Provides
    @Singleton
    fun provideWorkManager(@ApplicationContext context: Context): WorkManager =
        WorkManager.getInstance(context)

    /**
     * The production outbox scheduler: one unique WorkManager job with a
     * network constraint and exponential backoff. Unique-KEEP is safe against
     * the enqueue-during-drain race because [ChatStore.drainOutbox] re-reads
     * the queue until it is empty — the worker, not the scheduler, owns
     * completeness.
     */
    @Provides
    @Singleton
    fun provideOutboxScheduler(workManager: WorkManager): OutboxScheduler =
        object : OutboxScheduler {
            override fun scheduleDrain() {
                workManager.enqueueUniqueWork(
                    ChatStore.SEND_WORK_NAME,
                    androidx.work.ExistingWorkPolicy.KEEP,
                    androidx.work.OneTimeWorkRequestBuilder<ChatSendWorker>()
                        .setConstraints(
                            androidx.work.Constraints.Builder()
                                .setRequiredNetworkType(androidx.work.NetworkType.CONNECTED)
                                .build(),
                        )
                        .setBackoffCriteria(
                            androidx.work.BackoffPolicy.EXPONENTIAL,
                            SEND_BACKOFF_SECONDS,
                            java.util.concurrent.TimeUnit.SECONDS,
                        )
                        .build(),
                )
            }

            override fun cancelDrain() {
                workManager.cancelUniqueWork(ChatStore.SEND_WORK_NAME)
            }
        }

    private const val SEND_BACKOFF_SECONDS = 10L
}

@dagger.Module
@InstallIn(SingletonComponent::class)
abstract class ChatTeardownModule {

    /** Sign-out wipes the chat cache, outbox, socket and lock. */
    @dagger.Binds
    @dagger.multibindings.IntoSet
    abstract fun bindChatTeardown(
        impl: com.us.android.core.chat.data.ChatTeardown,
    ): com.us.android.core.common.session.SessionTeardownTask
}
