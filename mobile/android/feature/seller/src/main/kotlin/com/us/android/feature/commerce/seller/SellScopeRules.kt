package com.us.android.feature.commerce.seller

import com.us.android.core.commerce.model.SellScopeField
import com.us.android.core.commerce.model.SellScopeSaved
import com.us.android.core.commerce.model.panHolderLabel
import com.us.android.core.commerce.repository.CommerceError
import com.us.android.feature.commerce.ui.describe

/**
 * What MSeller says about the sell scope (commerce-service migration 045,
 * books-only sellers, founder 24 Oct 2026). Pure, so each sentence is a
 * table test.
 */

/** 403 SELLER_SCOPE_BOOKS_ONLY on a listing's create, edit or submit. */
const val BOOKS_ONLY_LISTING_COPY =
    "Your shop sells books only. List it in a book category at GST 0%, or add a GSTIN to sell in other categories."

/** Above the listing editor's pickers, for a books-only shop. */
const val BOOKS_ONLY_EDITOR_NOTE =
    "Your shop sells books only, so a listing goes in a book category at GST 0%."

/** After a books-only shop added its GSTIN. */
const val UPGRADE_DONE_COPY =
    "You can sell in every category now. New listings in other categories still go to review."

/** Beside the upgrade's GSTIN field: the server refuses any GSTIN but the one on the shop's PAN. */
const val UPGRADE_GSTIN_HELP = "It must be registered to the PAN your shop was approved with."

/** A refusal of the step or the upgrade: the field it is about (null for the whole form) and a sentence. */
data class SellScopeRefusal(val field: SellScopeField?, val message: String)

/**
 * The step's and the upgrade's refusals (handler_sellscope.go), each under
 * the field the seller has to change. Anything else is the shared commerce
 * copy, said for the whole form.
 */
fun sellScopeRefusal(error: CommerceError): SellScopeRefusal {
    val code = (error as? CommerceError.Unexpected)?.code
    return when (code) {
        "GSTIN_REQUIRED" -> SellScopeRefusal(SellScopeField.GSTIN, "Enter your GSTIN.")
        "GSTIN_INVALID" -> SellScopeRefusal(SellScopeField.GSTIN, "That is not a valid GSTIN. Check it for a typo.")
        "GSTIN_PAN_MISMATCH" -> SellScopeRefusal(
            SellScopeField.GSTIN,
            "This GSTIN is registered to a different PAN. Use the GSTIN issued on the PAN your shop was approved with.",
        )
        "PAN_REQUIRED" -> SellScopeRefusal(SellScopeField.PAN, "Enter your PAN.")
        "PAN_INVALID" -> SellScopeRefusal(SellScopeField.PAN, "That is not a valid PAN.")
        "BUSINESS_NAME_REQUIRED" ->
            SellScopeRefusal(SellScopeField.BUSINESS_NAME, "Enter the business name (2 to 200 characters).")
        "SELL_SCOPE_INVALID" -> SellScopeRefusal(null, "Choose what you will sell.")
        "SELL_SCOPE_FIELD_NOT_TAKEN" ->
            SellScopeRefusal(null, "Books only takes a PAN and no GSTIN; all categories take a GSTIN and no PAN.")
        "SELL_SCOPE_LOCKED" ->
            SellScopeRefusal(null, "Your application is with the reviewers, so what you sell cannot be changed now.")
        "SELL_SCOPE_NOT_BOOKS_ONLY" -> SellScopeRefusal(null, "Your shop already sells in every category.")
        else -> SellScopeRefusal(null, error.describe())
    }
}

/** What the step stored, as the seller sees it back: "PAN XXXXXX234F · Individual". Null when no PAN was stored. */
fun sellScopeSavedLine(saved: SellScopeSaved): String? {
    val masked = saved.panMasked?.takeIf { it.isNotBlank() } ?: return null
    val holder = saved.panHolderType?.takeIf { it.isNotBlank() }?.let(::panHolderLabel)
    return listOfNotNull("PAN $masked", holder).joinToString(" · ")
}
