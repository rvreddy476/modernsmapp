package com.us.android.feature.doorsteppro.store

import android.content.Context
import android.content.SharedPreferences
import com.us.android.feature.doorsteppro.deeplink.DigiLockerStateStore
import com.us.android.feature.doorsteppro.domain.OnboardingStep
import dagger.hilt.android.qualifiers.ApplicationContext
import java.time.Instant
import javax.inject.Inject
import javax.inject.Singleton

/*
 * Small on-device memories the contract has no route for. None is money or
 * identity: losing one only means the server answers the next request (a
 * refusal names what it still holds), never a wrong action.
 */

/** The DigiLocker `state` this device started, surviving the browser round trip and process death. */
@Singleton
class SharedPrefsDigiLockerStateStore @Inject constructor(
    @ApplicationContext context: Context,
) : DigiLockerStateStore {
    private val prefs: SharedPreferences = context.getSharedPreferences(FILE, Context.MODE_PRIVATE)

    override fun save(state: String) {
        prefs.edit().putString(KEY, state).apply()
    }

    override fun load(): String? = prefs.getString(KEY, null)

    override fun clear() {
        prefs.edit().remove(KEY).apply()
    }

    private companion object {
        const val FILE = "doorstep_pro_digilocker"
        const val KEY = "pending_state"
    }
}

/** Whether this install has shown, and the professional accepted, the location disclosure. */
interface LocationDisclosureStore {
    fun accepted(): Boolean
    fun markAccepted()
}

@Singleton
class SharedPrefsLocationDisclosureStore @Inject constructor(
    @ApplicationContext context: Context,
) : LocationDisclosureStore {
    private val prefs = context.getSharedPreferences("doorstep_pro_location_disclosure", Context.MODE_PRIVATE)

    override fun accepted(): Boolean = prefs.getBoolean(KEY, false)

    override fun markAccepted() {
        prefs.edit().putBoolean(KEY, true).apply()
    }

    private companion object {
        const val KEY = "accepted_v1"
    }
}

/**
 * Steps whose document this device sent for an admin review (a trade or
 * police certificate) — readiness says only "missing", and a professional
 * who just uploaded must see "in review", not "to do".
 *
 * Also what the professional saved that the contract cannot read back
 * (`PUT /pro/me/area`, `PUT /pro/me/bank` and `PUT /pro/me/skills` have no
 * GET): the masked bank account, the radius, the declared skills. Shown as
 * "saved" summaries; the server stays the source of truth through readiness.
 */
interface OnboardingMemory {
    fun submittedForReview(): Set<OnboardingStep>
    fun markSubmitted(step: OnboardingStep)
    fun clearSubmitted(step: OnboardingStep)

    fun bankSummary(): String?
    fun saveBankSummary(summary: String)

    fun radiusKm(): Int?
    fun saveRadiusKm(km: Int)

    fun declaredSkills(): Set<String>
    fun saveDeclaredSkills(codes: Set<String>)

    /** The trade certificates this device sent, by skill code. */
    fun certificateSkills(): Set<String>
    fun markCertificate(skillCode: String)

    fun clearAll()
}

@Singleton
class SharedPrefsOnboardingMemory @Inject constructor(
    @ApplicationContext context: Context,
) : OnboardingMemory {
    private val prefs = context.getSharedPreferences("doorstep_pro_onboarding", Context.MODE_PRIVATE)

    override fun submittedForReview(): Set<OnboardingStep> =
        prefs.getStringSet(SUBMITTED, emptySet()).orEmpty().mapNotNull(OnboardingStep::of).toSet()

    override fun markSubmitted(step: OnboardingStep) {
        prefs.edit().putStringSet(SUBMITTED, prefs.getStringSet(SUBMITTED, emptySet()).orEmpty() + step.wire).apply()
    }

    override fun clearSubmitted(step: OnboardingStep) {
        prefs.edit().putStringSet(SUBMITTED, prefs.getStringSet(SUBMITTED, emptySet()).orEmpty() - step.wire).apply()
    }

    override fun bankSummary(): String? = prefs.getString(BANK, null)

    override fun saveBankSummary(summary: String) {
        prefs.edit().putString(BANK, summary).apply()
    }

    override fun radiusKm(): Int? = prefs.getInt(RADIUS, 0).takeIf { it > 0 }

    override fun saveRadiusKm(km: Int) {
        prefs.edit().putInt(RADIUS, km).apply()
    }

    override fun declaredSkills(): Set<String> = prefs.getStringSet(SKILLS, emptySet()).orEmpty().toSet()

    override fun saveDeclaredSkills(codes: Set<String>) {
        prefs.edit().putStringSet(SKILLS, codes).apply()
    }

    override fun certificateSkills(): Set<String> = prefs.getStringSet(CERTIFICATES, emptySet()).orEmpty().toSet()

    override fun markCertificate(skillCode: String) {
        prefs.edit().putStringSet(CERTIFICATES, certificateSkills() + skillCode).apply()
    }

    override fun clearAll() {
        prefs.edit().clear().apply()
    }

    private companion object {
        const val SUBMITTED = "submitted_for_review"
        const val BANK = "bank_summary"
        const val RADIUS = "radius_km"
        const val SKILLS = "declared_skills"
        const val CERTIFICATES = "certificate_skills"
    }
}

/**
 * Per job: when this device marked "arrived" (the no-show wait counts from
 * it) and whether `finish` succeeded (ProJob has no flag for it). Kept a few
 * days, then dropped.
 */
interface VisitMemory {
    fun arrivedAt(bookingId: String): Instant?
    fun markArrived(bookingId: String, at: Instant)
    fun finished(bookingId: String): Boolean
    fun markFinished(bookingId: String)
}

@Singleton
class SharedPrefsVisitMemory @Inject constructor(
    @ApplicationContext context: Context,
) : VisitMemory {
    private val prefs = context.getSharedPreferences("doorstep_pro_visits", Context.MODE_PRIVATE)

    override fun arrivedAt(bookingId: String): Instant? =
        prefs.getLong(ARRIVED + bookingId, 0L).takeIf { it > 0 }?.let(Instant::ofEpochMilli)

    override fun markArrived(bookingId: String, at: Instant) {
        prune()
        prefs.edit().putLong(ARRIVED + bookingId, at.toEpochMilli()).apply()
    }

    override fun finished(bookingId: String): Boolean = prefs.getLong(FINISHED + bookingId, 0L) > 0

    override fun markFinished(bookingId: String) {
        prune()
        prefs.edit().putLong(FINISHED + bookingId, System.currentTimeMillis()).apply()
    }

    private fun prune() {
        val cutoff = System.currentTimeMillis() - KEEP_MILLIS
        val stale = prefs.all.filter { (_, v) -> v is Long && v < cutoff }.keys
        if (stale.isNotEmpty()) prefs.edit().apply { stale.forEach(::remove) }.apply()
    }

    private companion object {
        const val ARRIVED = "arrived:"
        const val FINISHED = "finished:"
        const val KEEP_MILLIS = 3L * 24 * 60 * 60 * 1000
    }
}
