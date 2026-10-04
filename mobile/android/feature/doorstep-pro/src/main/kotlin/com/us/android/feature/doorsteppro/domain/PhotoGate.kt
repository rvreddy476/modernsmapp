package com.us.android.feature.doorsteppro.domain

import com.us.android.feature.doorsteppro.data.PhotosRequiredDto
import com.us.android.feature.doorsteppro.data.ProCodes
import com.us.android.feature.doorsteppro.data.ProError
import com.us.android.feature.doorsteppro.data.code
import com.us.android.feature.doorsteppro.data.detailInt
import com.us.android.feature.doorsteppro.data.detailText

/** A job photo's phase (PhotoInput.phase). Workspace and kit only — never the customer. */
enum class PhotoPhase(val wire: String, val label: String) {
    BEFORE("before", "Before photos"),
    KIT_SEAL("kit_seal", "Sealed kit photo"),
    AFTER("after", "After photos"),
    EXTRA_EVIDENCE("extra_evidence", "Extra evidence"),
    ;

    companion object {
        fun of(wire: String?): PhotoPhase? = entries.firstOrNull { it.wire == wire }
    }
}

/** How many photos of each phase this job has on record. */
data class PhotoCounts(val counts: Map<PhotoPhase, Int> = emptyMap()) {
    operator fun get(phase: PhotoPhase): Int = counts[phase] ?: 0

    fun plus(phase: PhotoPhase): PhotoCounts = PhotoCounts(counts + (phase to get(phase) + 1))

    fun with(phase: PhotoPhase, count: Int): PhotoCounts = PhotoCounts(counts + (phase to count.coerceAtLeast(0)))
}

/**
 * The before/after photo gate, client side. The server is the gate
 * (DOORSTEP_PHOTOS_REQUIRED on start and complete); this keeps the button
 * honest so a professional is never sent to type a code the server will
 * refuse for a missing photo.
 *
 *  - start needs `photos_required.before` before photos and, for salon,
 *    `photos_required.kit_seal` sealed-kit photos;
 *  - complete needs `photos_required.after` after photos.
 *
 * When the server refuses anyway it says which phase and how many it holds
 * (`details.phase`, `details.uploaded`); [reconcile] takes its count, so a
 * device that lost its tally (process death) agrees with the server again.
 */
object PhotoGate {

    /** Phase → photos still needed to start. Empty means the gate is open. */
    fun missingToStart(required: PhotosRequiredDto, uploaded: PhotoCounts): Map<PhotoPhase, Int> = buildMap {
        val before = required.before - uploaded[PhotoPhase.BEFORE]
        if (before > 0) put(PhotoPhase.BEFORE, before)
        val kit = required.kitSeal - uploaded[PhotoPhase.KIT_SEAL]
        if (kit > 0) put(PhotoPhase.KIT_SEAL, kit)
    }

    /** Phase → photos still needed to complete. */
    fun missingToComplete(required: PhotosRequiredDto, uploaded: PhotoCounts): Map<PhotoPhase, Int> = buildMap {
        val after = required.after - uploaded[PhotoPhase.AFTER]
        if (after > 0) put(PhotoPhase.AFTER, after)
    }

    fun canStart(required: PhotosRequiredDto, uploaded: PhotoCounts): Boolean = missingToStart(required, uploaded).isEmpty()

    fun canComplete(required: PhotosRequiredDto, uploaded: PhotoCounts): Boolean = missingToComplete(required, uploaded).isEmpty()

    /** "Take 2 more before photos and 1 sealed kit photo." — null when nothing is missing. */
    fun missingText(missing: Map<PhotoPhase, Int>): String? {
        if (missing.isEmpty()) return null
        val parts = missing.entries.sortedBy { it.key.ordinal }.map { (phase, n) ->
            when (phase) {
                PhotoPhase.BEFORE -> "$n more before photo" + if (n == 1) "" else "s"
                PhotoPhase.AFTER -> "$n more after photo" + if (n == 1) "" else "s"
                PhotoPhase.KIT_SEAL -> "$n sealed kit photo" + if (n == 1) "" else "s"
                PhotoPhase.EXTRA_EVIDENCE -> "$n evidence photo" + if (n == 1) "" else "s"
            }
        }
        return "Take " + parts.joinToString(" and ") + "."
    }

    /** Adopts the server's count from a DOORSTEP_PHOTOS_REQUIRED refusal; anything else changes nothing. */
    fun reconcile(uploaded: PhotoCounts, error: ProError): PhotoCounts {
        if (error.code != ProCodes.PHOTOS_REQUIRED) return uploaded
        val phase = PhotoPhase.of(error.detailText("phase")) ?: return uploaded
        val serverCount = error.detailInt("uploaded") ?: return uploaded
        return uploaded.with(phase, serverCount)
    }
}
