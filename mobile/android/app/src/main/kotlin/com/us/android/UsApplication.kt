package com.us.android

import android.app.Application
import com.us.android.core.telemetry.Telemetry
import dagger.hilt.android.HiltAndroidApp
import javax.inject.Inject

/**
 * Application entry point and Hilt graph root.
 *
 * Deliberately close to empty. Anything expensive here lands directly on cold
 * start, which is a Phase 2 gate. Work that must happen early goes through
 * androidx.startup with an explicit Initializer so its cost is visible and
 * measurable, not hidden in onCreate.
 */
@HiltAndroidApp
class UsApplication : Application() {

    @Inject lateinit var telemetry: Telemetry

    override fun onCreate() {
        super.onCreate()
        installCrashReporter()
    }

    /**
     * Reports uncaught exceptions before the process dies.
     *
     * ⚠ This is NOT a substitute for a real crash-reporting product. It has no
     * symbolication, no deduplication, no grouping, and — most importantly —
     * export is best-effort: the OTLP batch processor may not flush before the
     * process is killed, so some crashes will be lost. It exists so a crash is
     * *usually* visible rather than *never*.
     *
     * A symbolicated, reliable crash pipeline needs a provisioned product
     * (Crashlytics or equivalent). That is a founder decision with a GCP
     * approval attached per decision-001, and is tracked as audit debt.
     *
     * The previous handler is always delegated to, so the platform still gets
     * to record the crash and show the dialog.
     */
    private fun installCrashReporter() {
        val previous = Thread.getDefaultUncaughtExceptionHandler()
        Thread.setDefaultUncaughtExceptionHandler { thread, throwable ->
            runCatching {
                telemetry.recordError(
                    event = "app.crash",
                    cause = throwable,
                    attributes = mapOf(
                        "thread.name" to thread.name,
                        "exception.type" to throwable.javaClass.name,
                    ),
                )
            }
            // Never swallow: the platform handler is what actually terminates
            // the process and records the ANR/crash for the OS.
            previous?.uncaughtException(thread, throwable)
        }
    }
}
