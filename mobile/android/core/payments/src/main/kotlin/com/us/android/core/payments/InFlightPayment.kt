package com.us.android.core.payments

/**
 * The smallest view of a saved-state bundle this module needs.
 *
 * Deliberately not `SavedStateHandle`, so the module carries no lifecycle
 * dependency and the in-flight record is testable on the JVM. A product adapts
 * its handle in two lines:
 *
 * ```kotlin
 * object : PaymentStateStore {
 *     override fun get(key: String): String? = handle[key]
 *     override fun set(key: String, value: String?) { handle[key] = value }
 * }
 * ```
 *
 * Whatever backs it is written to disk by the system, so nothing but the
 * attempt's identity is ever stored through it — never a provider session.
 */
interface PaymentStateStore {
    operator fun get(key: String): String?
    operator fun set(key: String, value: String?)
}

/**
 * The payment attempt that must survive process death, scoped to ONE
 * application.
 *
 * Android kills a backgrounded process at any time, and a buyer inside a
 * payment sheet is backgrounded by definition. The attempt is what keeps a late
 * callback from settling a newer attempt (C3-LB-4), and an attempt id that
 * resets on recreation defeats that.
 *
 * Every key is namespaced by [applicationId], and the stored record names its
 * application as well. So a pending Feast payment can never be resumed or shown
 * inside MStore checkout, and vice versa — even if both were ever written
 * through the same store: reading under one application sees only its own
 * record, and writing another application's attempt is refused.
 */
class InFlightPayment(
    private val store: PaymentStateStore,
    applicationId: String,
) {
    val applicationId: String = PaymentApplication.requireValid(applicationId)

    private val keyApplication = "payments.$applicationId.applicationId"
    private val keyReference = "payments.$applicationId.referenceId"
    private val keyAttempt = "payments.$applicationId.attemptId"

    /**
     * This application's in-flight attempt, or null.
     *
     * @throws IllegalArgumentException on writing an attempt that belongs to
     *   another application.
     */
    var attempt: PaymentAttempt?
        get() {
            // A record that does not name THIS application is not ours, however
            // it got there.
            if (store[keyApplication] != applicationId) return null
            val reference = store[keyReference] ?: return null
            val id = store[keyAttempt] ?: return null
            return PaymentAttempt(applicationId = applicationId, referenceId = reference, id = id)
        }
        set(value) {
            require(value == null || value.applicationId == applicationId) {
                "a ${value?.applicationId} attempt cannot be saved as $applicationId's in-flight payment"
            }
            store[keyApplication] = value?.applicationId
            store[keyReference] = value?.referenceId
            store[keyAttempt] = value?.id
        }

    /** Forgets this application's in-flight attempt. Other applications' records are untouched. */
    fun clear() {
        attempt = null
    }
}
