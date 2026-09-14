package com.us.android.feature.commerce.checkout

/**
 * MStore's application id in `:core:payments` — the ONE place commerce names it.
 *
 * Every payment this module opens, confirms or persists belongs to this
 * application, so a pending payment from another application (Feast) can never
 * be resumed or shown in MStore checkout.
 *
 * PROVISIONAL (founder requirement, 2026-09-14): the key becomes whatever the
 * server-side application registry assigns once it exists. It is client-side
 * only today and is sent to no endpoint; see `PaymentApplication` in
 * `:core:payments` for where it will be sent.
 */
internal const val MSTORE_PAYMENT_APPLICATION_ID = "mstore"
