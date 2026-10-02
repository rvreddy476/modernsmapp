package com.us.android.feature.live.data

import com.us.android.core.common.error.AppError
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json

/*
 * Who may go live (live-eligibility contract, 2026-10-02). The server
 * decides; these functions only turn its answer into what the go-live
 * screen shows first: the form, the closed-pilot notice, or the list of
 * what is still needed.
 */

/** The requirement keys the contract names. An unknown key is still shown, in general words. */
const val REQ_PHONE_VERIFIED = "phone_verified"
const val REQ_ADULT = "adult"
const val REQ_ACCOUNT_AGE = "account_age"
const val REQ_ACTIVITY = "activity"
const val REQ_GOOD_STANDING = "good_standing"

/** 403 on create/start in `open` mode, with `details.requirements`. */
const val CODE_LIVE_NOT_ELIGIBLE = "LIVE_NOT_ELIGIBLE"

/** 403 on the viewer token when a new streamer's viewer cap is reached. */
const val CODE_STREAM_FULL = "STREAM_FULL"

/** 503 on create/start when a requirement could not be checked (the server fails closed). */
const val CODE_AUTHORITY_UNAVAILABLE = "AUTHORITY_UNAVAILABLE"

/** `met` is true, false, or null when the server could not check it right now. */
enum class RequirementState { Met, Unmet, Unknown }

val LiveRequirementDto.state: RequirementState
    get() = when (met) {
        true -> RequirementState.Met
        false -> RequirementState.Unmet
        null -> RequirementState.Unknown
    }

/** What the one primary button on the "not yet" screen does. */
enum class LiveGateAction {
    /** `activity` is unmet: open the create flow. */
    CreatePost,

    /** `phone_verified` is unmet: open the account screen that shows the phone number. */
    VerifyPhone,

    /** Nothing the user can do here but wait, or a requirement could not be checked: ask again. */
    CheckAgain,
}

/** What the go-live screen opens on. */
sealed interface LiveGate {
    /** The form. [viewerCap] is the new-streamer cap, or 0 when none applies. */
    data class Open(val viewerCap: Int = 0) : LiveGate

    /** Live is in its closed pilot and this user is not on the list. */
    data object PilotOnly : LiveGate

    /** Not yet: each requirement, and the button that helps the first one still unmet. */
    data class NotYet(
        val requirements: List<LiveRequirementDto>,
        val action: LiveGateAction,
    ) : LiveGate
}

/**
 * The gate for an eligibility answer.
 *
 *  - `eligible` → the form.
 *  - not eligible in the pilot (`pilot_only`, or `mode: pilot`) → the pilot notice.
 *  - not eligible with something to explain → the list.
 *  - not eligible with NOTHING to explain (no requirement is unmet or
 *    unknown) → the form: the client has no sentence to show, and the server
 *    still decides on create and start.
 */
fun liveGateOf(answer: LiveEligibilityDto): LiveGate = when {
    answer.eligible -> LiveGate.Open(viewerCap = answer.viewerCap.coerceAtLeast(0))
    answer.pilotOnly || answer.mode.trim().equals("pilot", ignoreCase = true) -> LiveGate.PilotOnly
    else -> notYetOf(answer.requirements) ?: LiveGate.Open()
}

/** The list for [requirements], or null when none of them is unmet or unknown. */
fun notYetOf(requirements: List<LiveRequirementDto>): LiveGate.NotYet? {
    val rows = requirements.filter { it.key.isNotBlank() }
    if (rows.none { it.state != RequirementState.Met }) return null
    return LiveGate.NotYet(requirements = rows, action = gateActionFor(rows))
}

/**
 * The button: the action that helps the FIRST unmet requirement the user can
 * do something about, in the server's order. Age, account age and standing
 * have no action; with only those left (or only unknowns) the button asks
 * the server again.
 */
fun gateActionFor(requirements: List<LiveRequirementDto>): LiveGateAction =
    requirements.asSequence()
        .filter { it.state == RequirementState.Unmet }
        .mapNotNull { requirement ->
            when (requirement.key) {
                REQ_PHONE_VERIFIED -> LiveGateAction.VerifyPhone
                REQ_ACTIVITY -> LiveGateAction.CreatePost
                else -> null
            }
        }
        .firstOrNull() ?: LiveGateAction.CheckAgain

/**
 * The same list from a refused create or start: `403 LIVE_NOT_ELIGIBLE`
 * carries `details.requirements`, which the error mapper keeps as JSON text.
 * Null when the error is anything else, or carries nothing readable — the
 * caller then falls back to its one-line refusal.
 */
fun notYetFromRefusal(error: AppError, json: Json): LiveGate.NotYet? {
    if (error.liveCode() != CODE_LIVE_NOT_ELIGIBLE) return null
    val raw = (error as? AppError.Forbidden)?.details?.get("requirements")?.takeIf { it.isNotBlank() } ?: return null
    val requirements = runCatching {
        json.decodeFromString(ListSerializer(LiveRequirementDto.serializer()), raw)
    }.getOrNull() ?: return null
    return notYetOf(requirements)
}
