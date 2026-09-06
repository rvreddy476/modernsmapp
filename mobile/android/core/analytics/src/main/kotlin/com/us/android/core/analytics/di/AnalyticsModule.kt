package com.us.android.core.analytics.di

import androidx.work.BackoffPolicy
import androidx.work.Constraints
import androidx.work.ExistingWorkPolicy
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.WorkManager
import com.us.android.core.analytics.AnalyticsClient
import com.us.android.core.analytics.AnalyticsRecorder
import com.us.android.core.analytics.data.AnalyticsApi
import com.us.android.core.analytics.data.AnalyticsStore
import com.us.android.core.analytics.data.AnalyticsTeardown
import com.us.android.core.analytics.data.AnalyticsUploadScheduler
import com.us.android.core.analytics.data.AnalyticsUploadWorker
import com.us.android.core.common.session.SessionTeardownTask
import dagger.Binds
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import dagger.multibindings.IntoSet
import retrofit2.Retrofit
import java.util.concurrent.TimeUnit
import javax.inject.Singleton

/**
 * Wires the analytics client to the app-wide Retrofit and WorkManager.
 *
 * No client, no base URL, no converter of its own — the same rule `:core:chat`
 * and `:core:commerce` state. A module that assembles its own OkHttp forks
 * token refresh, and two refreshers racing a rotating refresh token sign the
 * user out. Telemetry causing a sign-out would be an absurd way to lose an
 * account.
 */
@Module
@InstallIn(SingletonComponent::class)
object AnalyticsModule {

    @Provides
    @Singleton
    fun provideAnalyticsApi(retrofit: Retrofit): AnalyticsApi = retrofit.create(AnalyticsApi::class.java)

    /**
     * One unique job, network-constrained, exponential backoff.
     *
     * `KEEP` rather than `REPLACE`: the enqueue-during-drain race is safe
     * because [AnalyticsStore.drain] re-reads the queue until it is empty, so
     * the worker — not the scheduler — owns completeness. `REPLACE` would
     * cancel a drain that was midway through delivering a batch.
     *
     * The network constraint is what keeps this off the radio when there is no
     * connection: an analytics flush is never worth waking a modem for.
     */
    @Provides
    @Singleton
    fun provideAnalyticsUploadScheduler(workManager: WorkManager): AnalyticsUploadScheduler =
        object : AnalyticsUploadScheduler {
            override fun scheduleUpload() {
                workManager.enqueueUniqueWork(
                    AnalyticsStore.UPLOAD_WORK_NAME,
                    ExistingWorkPolicy.KEEP,
                    OneTimeWorkRequestBuilder<AnalyticsUploadWorker>()
                        .setConstraints(
                            Constraints.Builder()
                                .setRequiredNetworkType(NetworkType.CONNECTED)
                                .build(),
                        )
                        .setBackoffCriteria(
                            BackoffPolicy.EXPONENTIAL,
                            UPLOAD_BACKOFF_SECONDS,
                            TimeUnit.SECONDS,
                        )
                        .build(),
                )
            }

            override fun cancelUpload() {
                workManager.cancelUniqueWork(AnalyticsStore.UPLOAD_WORK_NAME)
            }
        }

    /**
     * Base backoff.
     *
     * Thirty seconds rather than the ten `:core:chat` uses for sends: a chat
     * message the user is watching for should come back quickly, and nobody is
     * waiting on an analytics event. Across [AnalyticsStore.MAX_ATTEMPTS] the
     * exponential schedule then spans hours, which outlasts a tunnel or a
     * service restart without hammering either.
     */
    private const val UPLOAD_BACKOFF_SECONDS = 30L
}

@Module
@InstallIn(SingletonComponent::class)
abstract class AnalyticsBindings {

    /** Sign-out flushes what it can while the token is still valid, then wipes. */
    @Binds
    @IntoSet
    abstract fun bindAnalyticsTeardown(impl: AnalyticsTeardown): SessionTeardownTask

    /** [VideoWatchTracker] depends on the interface so its rules stay testable. */
    @Binds
    abstract fun bindAnalyticsRecorder(impl: AnalyticsClient): AnalyticsRecorder
}
